// Package main — raw_spawn.go implements the L2 pipe-only raw subprocess capture.
//
// runRawCLI reimplements the spawn loop from pkg/executor/pipe/pipe.go with one
// key mutation: io.TeeReader is inserted BEFORE the line scanner so raw bytes
// reach the JSONL log pre-StripANSI, pre-redact.
//
// Pipeline topology per stream (stdout shown; stderr is symmetric):
//
//	handle.Stdout ──► TeeReader(rawPW) ──► teeOut ──► stripped scanner goroutine
//	                       │
//	                      rawPW
//	                       │
//	                      rawPR ──► emitRawLines goroutine
//
// Memory discipline (NFR-8): each line is emitted immediately — no accumulation.
package main

import (
	"bufio"
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/thebtf/aimux/pkg/executor"
	"github.com/thebtf/aimux/pkg/executor/pipeline"
	"github.com/thebtf/aimux/pkg/types"
)

// rawSpawnMaxLineBytes is the maximum single-line buffer for the raw scanner (1 MB).
const rawSpawnMaxLineBytes = 1024 * 1024

// runRawCLI executes spawnArgs as a subprocess via SharedPM, inserts TeeReaders
// on stdout and stderr to capture raw bytes pre-StripANSI, and emits events through sink.
//
//   - ctx     — deadline/timeout context → exit code 124 on expiry
//   - sigCtx  — signal-cancel context (Ctrl+C / SIGTERM) → exit code 130 on expiry
func runRawCLI(ctx context.Context, sigCtx context.Context, spawnArgs types.SpawnArgs, sink EventSink) (int, error) {
	start := time.Now()

	cmd := exec.Command(spawnArgs.Command, spawnArgs.Args...)
	cmd.Dir = spawnArgs.CWD
	switch {
	case len(spawnArgs.EnvList) > 0:
		cmd.Env = spawnArgs.EnvList
	case len(spawnArgs.Env) > 0:
		cmd.Env = rawMergeEnv(spawnArgs.Env)
	}
	if spawnArgs.Stdin != "" {
		cmd.Stdin = strings.NewReader(spawnArgs.Stdin)
	}

	sink.Emit(KindSpawnArgs, spawnArgsPayload{
		Command:  spawnArgs.Command,
		Args:     spawnArgs.Args,
		CWD:      spawnArgs.CWD,
		Executor: "pipe/raw",
	})

	handle, err := executor.SharedPM.Spawn(cmd)
	if err != nil {
		return -1, fmt.Errorf("spawn %s: %w", spawnArgs.Command, err)
	}
	defer executor.SharedPM.Cleanup(handle)

	sink.Emit(KindSpawn, spawnPayload{PID: handle.PID, Command: spawnArgs.Command})

	var wg sync.WaitGroup

	// stdout pipeline — collect stripped lines for pattern matching
	var stdoutLines []string
	var stdoutMu sync.Mutex
	startStreamPair(&wg, sink, handle.Stdout, KindStdout, &stdoutLines, &stdoutMu)

	// stderr pipeline — lines discarded after emit (no pattern matching on stderr)
	startStreamPair(&wg, sink, handle.Stderr, KindStderr, nil, nil)

	// Completion pattern watcher
	patternMatched := make(chan struct{}, 1)
	if spawnArgs.CompletionPattern != "" {
		go watchPattern(spawnArgs.CompletionPattern, &stdoutLines, &stdoutMu, patternMatched)
	}

	// Timeout channel
	var timerC <-chan time.Time
	if spawnArgs.TimeoutSeconds > 0 {
		timer := time.NewTimer(time.Duration(spawnArgs.TimeoutSeconds) * time.Second)
		defer timer.Stop()
		timerC = timer.C
	}

	// 4-way select — mirrors pipe.go:94-154
	var exitCode int
	select {
	case waitErr := <-handle.Done:
		wg.Wait()
		exitCode = 0
		if waitErr != nil {
			if exitErr, ok := waitErr.(*exec.ExitError); ok {
				exitCode = exitErr.ExitCode()
			} else {
				sink.Emit(KindExit, exitPayload{PID: handle.PID, ExitCode: -1})
				return -1, fmt.Errorf("%s wait: %w", spawnArgs.Command, waitErr)
			}
		}
	case <-patternMatched:
		executor.SharedPM.Kill(handle)
		wg.Wait()
		exitCode = 0
	case <-timerC:
		executor.SharedPM.Kill(handle)
		wg.Wait()
		exitCode = 124
	case <-ctx.Done():
		executor.SharedPM.Kill(handle)
		wg.Wait()
		exitCode = 130
	case <-sigCtx.Done():
		executor.SharedPM.Kill(handle)
		wg.Wait()
		exitCode = 130
	}

	_ = time.Since(start) // reserved for future exit payload extension
	sink.Emit(KindExit, exitPayload{PID: handle.PID, ExitCode: exitCode})
	return exitCode, nil
}

// startStreamPair wires a TeeReader split for one stream (stdout or stderr).
// It launches two goroutines — one for raw hex events and one for stripped line events.
// If lines/mu are non-nil, stripped lines are appended to *lines for pattern matching.
func startStreamPair(
	wg *sync.WaitGroup,
	sink EventSink,
	src io.ReadCloser,
	kind string,
	lines *[]string,
	mu *sync.Mutex,
) {
	rawPR, rawPW := io.Pipe()
	teeOut := io.TeeReader(src, rawPW)

	// Raw goroutine: reads rawPR, emits one "raw" event per line immediately.
	wg.Add(1)
	go func() {
		defer wg.Done()
		emitRawLines(sink, kind, rawPR)
	}()

	// Stripped goroutine: reads teeOut (which forwards bytes to rawPW),
	// strips ANSI, emits "line" events immediately.
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer rawPW.Close() // EOF signal to raw goroutine when teeOut is drained

		sc := bufio.NewScanner(teeOut)
		sc.Buffer(make([]byte, 0, 64*1024), rawSpawnMaxLineBytes)
		for sc.Scan() {
			line := pipeline.StripANSI(sc.Text())
			switch kind {
			case KindStdout:
				sink.Emit(KindStdout, stdoutPayload{Stream: "line", Content: line})
			case KindStderr:
				sink.Emit(KindStderr, stderrPayload{Stream: "line", Content: line})
			}
			if lines != nil && mu != nil {
				mu.Lock()
				*lines = append(*lines, line)
				mu.Unlock()
			}
		}
	}()
}

// emitRawLines reads from r line-by-line and emits one raw event per line.
// Each line is emitted immediately — satisfies NFR-8 (bounded memory).
func emitRawLines(sink EventSink, kind string, r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), rawSpawnMaxLineBytes)
	for sc.Scan() {
		// Copy bytes: sc.Bytes() shares the internal scanner buffer.
		cp := make([]byte, len(sc.Bytes()))
		copy(cp, sc.Bytes())
		hexStr := hex.EncodeToString(cp)
		switch kind {
		case KindStdout:
			sink.Emit(KindStdout, stdoutPayload{Stream: "raw", BytesHex: hexStr})
		case KindStderr:
			sink.Emit(KindStderr, stderrPayload{Stream: "raw", BytesHex: hexStr})
		}
	}
}

// watchPattern polls lines for the first occurrence of pattern and sends on matched.
func watchPattern(pattern string, lines *[]string, mu *sync.Mutex, matched chan<- struct{}) {
	for {
		mu.Lock()
		for _, l := range *lines {
			if strings.Contains(l, pattern) {
				mu.Unlock()
				select {
				case matched <- struct{}{}:
				default:
				}
				return
			}
		}
		mu.Unlock()
		time.Sleep(50 * time.Millisecond)
	}
}

// rawMergeEnv merges extra key=value pairs into a copy of os.Environ.
func rawMergeEnv(extra map[string]string) []string {
	env := os.Environ()
	for k, v := range extra {
		env = append(env, k+"="+v)
	}
	return env
}
