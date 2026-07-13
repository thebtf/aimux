package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	genericWorkerMaxFloodChunks     = 4096
	genericWorkerMaxFloodChunkBytes = 64 << 10
	genericWorkerMaxFloodBytes      = 1 << 20
	genericWorkerMaxTreeDepth       = 8
	genericWorkerMaxTreeHoldMS      = 10_000
	genericWorkerTreeChildEnv       = "AIMUX_TESTCLI_INTERNAL_TREE_NODE"
)

type genericWorkerFloodResult struct {
	channel string
	failed  bool
}

func writeGenericWorkerChunk(writer io.Writer, data []byte) error {
	written, err := writer.Write(data)
	if err != nil {
		return err
	}
	if written != len(data) {
		return io.ErrShortWrite
	}
	return nil
}

func writeGenericWorkerChunks(writer io.Writer, chunks ...[]byte) error {
	for _, chunk := range chunks {
		if err := writeGenericWorkerChunk(writer, chunk); err != nil {
			return err
		}
	}
	return nil
}

func reportGenericWorkerWriteFailure(stderr io.Writer, mode, channel string) {
	// Best effort: stderr may itself be the failed sink. The non-zero exit code
	// remains authoritative even when the diagnostic cannot be delivered.
	_, _ = fmt.Fprintf(stderr, "generic-worker: %s %s write failed\n", mode, channel)
}

// runGenericWorker runs the provider-neutral process emulator against explicit
// output channels so tests can prove stdout/stderr separation without globals.
func runGenericWorker(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("generic-worker", flag.ContinueOnError)
	flags.SetOutput(stderr)
	mode := flags.String("mode", "stream", "emulation mode")
	count := flags.Int("count", 64, "flood chunks per channel")
	chunkBytes := flags.Int("chunk-bytes", 256, "bytes per flood chunk")
	depth := flags.Int("depth", 2, "tree descendant depth")
	level := flags.Int("level", 0, "internal tree node level")
	holdMS := flags.Int("hold-ms", 50, "tree leaf lifetime in milliseconds")
	rootExit := flags.Bool("root-exit", false, "tree root exits while its descendant retains output pipes")
	if err := flags.Parse(args); err != nil {
		return 2
	}

	switch *mode {
	case "stream":
		stdoutErr := writeGenericWorkerChunk(stdout, []byte("stdout:alpha\nstdout:omega\n"))
		stderrErr := writeGenericWorkerChunk(stderr, []byte("stderr:alpha\nstderr:omega\n"))
		if stdoutErr != nil {
			reportGenericWorkerWriteFailure(stderr, *mode, "stdout")
		}
		if stderrErr != nil {
			reportGenericWorkerWriteFailure(stderr, *mode, "stderr")
		}
		if stdoutErr != nil || stderrErr != nil {
			return 1
		}
		return 0
	case "flood":
		if *count < 1 || *count > genericWorkerMaxFloodChunks ||
			*chunkBytes < 2 || *chunkBytes > genericWorkerMaxFloodChunkBytes ||
			*count > genericWorkerMaxFloodBytes / *chunkBytes {
			fmt.Fprintf(
				stderr,
				"generic-worker: flood bounds: count=1..%d chunk-bytes=2..%d total-bytes<=%d per channel\n",
				genericWorkerMaxFloodChunks,
				genericWorkerMaxFloodChunkBytes,
				genericWorkerMaxFloodBytes,
			)
			return 2
		}
		stdoutChunk := []byte(strings.Repeat("O", *chunkBytes-1) + "\n")
		stderrChunk := []byte(strings.Repeat("E", *chunkBytes-1) + "\n")
		var writers sync.WaitGroup
		results := make(chan genericWorkerFloodResult, 2)
		writers.Add(2)
		go func(channel string, writer io.Writer, chunk []byte) {
			defer writers.Done()
			for range *count {
				if err := writeGenericWorkerChunk(writer, chunk); err != nil {
					results <- genericWorkerFloodResult{channel: channel, failed: true}
					return
				}
			}
			results <- genericWorkerFloodResult{channel: channel}
		}("stdout", stdout, stdoutChunk)
		go func(channel string, writer io.Writer, chunk []byte) {
			defer writers.Done()
			for range *count {
				if err := writeGenericWorkerChunk(writer, chunk); err != nil {
					results <- genericWorkerFloodResult{channel: channel, failed: true}
					return
				}
			}
			results <- genericWorkerFloodResult{channel: channel}
		}("stderr", stderr, stderrChunk)
		writers.Wait()
		close(results)
		failed := map[string]bool{}
		for result := range results {
			failed[result.channel] = result.failed
		}
		for _, channel := range []string{"stdout", "stderr"} {
			if failed[channel] {
				reportGenericWorkerWriteFailure(stderr, *mode, channel)
			}
		}
		if failed["stdout"] || failed["stderr"] {
			return 1
		}
		return 0
	case "framing":
		stdoutErr := writeGenericWorkerChunks(
			stdout,
			[]byte("utf8:"),
			[]byte{0xce},
			[]byte{0xb2},
			[]byte("\r\ncr-only\rline-feed\ninvalid:"),
			[]byte{0xff, 0xfe},
			[]byte("\ncontrol:"),
			[]byte{0x00, 0x1b},
			[]byte("\nno-final-newline"),
		)
		stderrErr := writeGenericWorkerChunks(
			stderr,
			[]byte("stderr-crlf\r\nstderr-invalid:"),
			[]byte{0xff},
			[]byte("\nstderr-control:"),
			[]byte{0x00},
			[]byte("\nstderr-no-final-newline"),
		)
		if stdoutErr != nil {
			reportGenericWorkerWriteFailure(stderr, *mode, "stdout")
		}
		if stderrErr != nil {
			reportGenericWorkerWriteFailure(stderr, *mode, "stderr")
		}
		if stdoutErr != nil || stderrErr != nil {
			return 1
		}
		return 0
	case "tree":
		if *depth < 0 || *depth > genericWorkerMaxTreeDepth ||
			*level != 0 ||
			(*rootExit && *depth == 0) ||
			*holdMS < 0 || *holdMS > genericWorkerMaxTreeHoldMS {
			fmt.Fprintf(
				stderr,
				"generic-worker: tree bounds: depth=0..%d level=0..depth hold-ms=0..%d\n",
				genericWorkerMaxTreeDepth,
				genericWorkerMaxTreeHoldMS,
			)
			return 2
		}
		return runGenericWorkerTree(stdout, stderr, *depth, *level, *holdMS, *rootExit)
	case "tree-node":
		if os.Getenv(genericWorkerTreeChildEnv) != "1" ||
			*depth < 1 || *depth > genericWorkerMaxTreeDepth ||
			*level < 1 || *level > *depth ||
			*rootExit ||
			*holdMS < 0 || *holdMS > genericWorkerMaxTreeHoldMS {
			fmt.Fprintln(stderr, "generic-worker: rejected internal tree-node invocation")
			return 2
		}
		return runGenericWorkerTree(stdout, stderr, *depth, *level, *holdMS, false)
	default:
		fmt.Fprintf(stderr, "generic-worker: unknown mode %q\n", *mode)
		return 2
	}
}

type genericWorkerTreeEvent struct {
	Event     string `json:"event"`
	Level     int    `json:"level,omitempty"`
	PID       int    `json:"pid,omitempty"`
	ParentPID int    `json:"parent_pid,omitempty"`
	Nodes     int    `json:"nodes,omitempty"`
}

func runGenericWorkerTree(stdout, stderr io.Writer, depth, level, holdMS int, rootExit bool) int {
	encoder := json.NewEncoder(stdout)
	pid := os.Getpid()
	if err := encoder.Encode(genericWorkerTreeEvent{
		Event:     "tree.node",
		Level:     level,
		PID:       pid,
		ParentPID: os.Getppid(),
	}); err != nil {
		reportGenericWorkerWriteFailure(stderr, "tree", "stdout")
		return 1
	}
	if err := writeGenericWorkerChunk(stderr, []byte(fmt.Sprintf(
		"tree node level=%d pid=%d parent_pid=%d\n",
		level,
		pid,
		os.Getppid(),
	))); err != nil {
		reportGenericWorkerWriteFailure(stderr, "tree", "stderr")
		return 1
	}

	if level < depth {
		executable, err := os.Executable()
		if err != nil {
			fmt.Fprintf(stderr, "generic-worker: resolve executable: %v\n", err)
			return 1
		}
		child := exec.Command(
			executable,
			"generic-worker",
			"--mode", "tree-node",
			"--depth", strconv.Itoa(depth),
			"--level", strconv.Itoa(level+1),
			"--hold-ms", strconv.Itoa(holdMS),
		)
		child.Env = append(os.Environ(), genericWorkerTreeChildEnv+"=1")
		child.Stdout = stdout
		child.Stderr = stderr
		if rootExit && level == 0 {
			if err := child.Start(); err != nil {
				fmt.Fprintf(stderr, "generic-worker: start detached tree child level=%d: %v\n", level+1, err)
				return 1
			}
			if err := child.Process.Release(); err != nil {
				_ = child.Process.Kill()
				_, _ = child.Process.Wait()
				fmt.Fprintf(stderr, "generic-worker: release detached tree child level=%d: %v\n", level+1, err)
				return 1
			}
			return 0
		}
		if err := child.Run(); err != nil {
			fmt.Fprintf(stderr, "generic-worker: tree child level=%d: %v\n", level+1, err)
			return 1
		}
	} else {
		time.Sleep(time.Duration(holdMS) * time.Millisecond)
	}

	if level == 0 {
		if err := encoder.Encode(genericWorkerTreeEvent{Event: "tree.complete", Nodes: depth + 1}); err != nil {
			reportGenericWorkerWriteFailure(stderr, "tree", "stdout")
			return 1
		}
	}
	return 0
}
