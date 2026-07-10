package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

type recordingWriter struct {
	bytes.Buffer
	writes [][]byte
}

type rejectingWriter struct {
	writes int
}

type rendezvousWriter struct {
	name    string
	started chan<- string
	release <-chan struct{}
	once    sync.Once
}

func (writer *rendezvousWriter) Write(data []byte) (int, error) {
	writer.once.Do(func() {
		writer.started <- writer.name
		<-writer.release
	})
	return len(data), nil
}

func (writer *rejectingWriter) Write(_ []byte) (int, error) {
	writer.writes++
	return 0, fmt.Errorf("unexpected write")
}

func (writer *recordingWriter) Write(data []byte) (int, error) {
	writer.writes = append(writer.writes, bytes.Clone(data))
	return writer.Buffer.Write(data)
}

func (writer *recordingWriter) hasWrite(want []byte) bool {
	for _, write := range writer.writes {
		if bytes.Equal(write, want) {
			return true
		}
	}
	return false
}

func TestGenericWorker_StreamSeparatesChannels(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := runGenericWorker([]string{"--mode", "stream"}, &stdout, &stderr)

	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", exitCode, stderr.String())
	}
	if got, want := stdout.String(), "stdout:alpha\nstdout:omega\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if got, want := stderr.String(), "stderr:alpha\nstderr:omega\n"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
}

func TestGenericWorker_FloodWritesFixedChunksToBothChannels(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := runGenericWorker(
		[]string{"--mode", "flood", "--count", "3", "--chunk-bytes", "8"},
		&stdout,
		&stderr,
	)

	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", exitCode, stderr.String())
	}
	if got, want := stdout.String(), strings.Repeat("OOOOOOO\n", 3); got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if got, want := stderr.String(), strings.Repeat("EEEEEEE\n", 3); got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
}

func TestGenericWorker_FloodRejectsUnboundedRequests(t *testing.T) {
	testCases := []struct {
		name string
		args []string
	}{
		{name: "too many chunks", args: []string{"--mode", "flood", "--count", "4097", "--chunk-bytes", "2"}},
		{name: "oversized chunk", args: []string{"--mode", "flood", "--count", "1", "--chunk-bytes", "65537"}},
		{name: "oversized total", args: []string{"--mode", "flood", "--count", "17", "--chunk-bytes", "65536"}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer

			exitCode := runGenericWorker(testCase.args, &stdout, &stderr)

			if exitCode != 2 {
				t.Fatalf("exit code = %d, want 2", exitCode)
			}
			if stdout.Len() != 0 {
				t.Fatalf("stdout length = %d, want 0", stdout.Len())
			}
			if !strings.Contains(stderr.String(), "flood bounds:") {
				t.Fatalf("stderr = %q, want flood bounds diagnostic", stderr.String())
			}
		})
	}
}

func TestGenericWorker_FloodStartsBothChannelsConcurrently(t *testing.T) {
	started := make(chan string, 2)
	release := make(chan struct{})
	stdout := &rendezvousWriter{name: "stdout", started: started, release: release}
	stderr := &rendezvousWriter{name: "stderr", started: started, release: release}
	exitCode := make(chan int, 1)
	go func() {
		exitCode <- runGenericWorker(
			[]string{"--mode", "flood", "--count", "1", "--chunk-bytes", "8"},
			stdout,
			stderr,
		)
	}()

	seen := map[string]bool{}
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	for len(seen) < 2 {
		select {
		case channel := <-started:
			seen[channel] = true
		case <-timer.C:
			close(release)
			<-exitCode
			t.Fatalf("channels did not start concurrently; started=%v", seen)
		}
	}
	close(release)
	if got := <-exitCode; got != 0 {
		t.Fatalf("exit code = %d, want 0", got)
	}
}

func TestGenericWorker_OutputSinkFailureReturnsNonZero(t *testing.T) {
	testCases := []struct {
		name string
		args []string
	}{
		{name: "stream", args: []string{"--mode", "stream"}},
		{name: "flood", args: []string{"--mode", "flood", "--count", "3", "--chunk-bytes", "8"}},
		{name: "framing", args: []string{"--mode", "framing"}},
		{name: "tree", args: []string{"--mode", "tree", "--depth", "0", "--hold-ms", "0"}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			for _, failedChannel := range []string{"stdout", "stderr"} {
				t.Run(failedChannel, func(t *testing.T) {
					var stdout bytes.Buffer
					var stderr bytes.Buffer
					rejected := &rejectingWriter{}
					stdoutWriter := io.Writer(&stdout)
					stderrWriter := io.Writer(&stderr)
					if failedChannel == "stdout" {
						stdoutWriter = rejected
					} else {
						stderrWriter = rejected
					}

					exitCode := runGenericWorker(testCase.args, stdoutWriter, stderrWriter)

					if exitCode != 1 {
						t.Fatalf("exit code = %d, want 1 after %s failure", exitCode, failedChannel)
					}
					if rejected.writes == 0 {
						t.Fatalf("%s rejecting writer was never exercised", failedChannel)
					}
					if failedChannel == "stdout" && !strings.Contains(stderr.String(), "write failed") {
						t.Fatalf("stderr = %q, want safe write-failure diagnostic", stderr.String())
					}
				})
			}
		})
	}
}

func TestGenericWorker_FramingEmitsByteEdgeCases(t *testing.T) {
	var stdout recordingWriter
	var stderr recordingWriter

	exitCode := runGenericWorker([]string{"--mode", "framing"}, &stdout, &stderr)

	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", exitCode, stderr.String())
	}
	wantStdout := append([]byte("utf8:"), 0xce, 0xb2)
	wantStdout = append(wantStdout, []byte("\r\ncr-only\rline-feed\ninvalid:")...)
	wantStdout = append(wantStdout, 0xff, 0xfe)
	wantStdout = append(wantStdout, []byte("\ncontrol:")...)
	wantStdout = append(wantStdout, 0x00, 0x1b)
	wantStdout = append(wantStdout, []byte("\nno-final-newline")...)
	if got := stdout.Bytes(); !bytes.Equal(got, wantStdout) {
		t.Fatalf("stdout bytes = %v, want %v", got, wantStdout)
	}
	wantStderr := []byte("stderr-crlf\r\nstderr-invalid:")
	wantStderr = append(wantStderr, 0xff)
	wantStderr = append(wantStderr, []byte("\nstderr-control:")...)
	wantStderr = append(wantStderr, 0x00)
	wantStderr = append(wantStderr, []byte("\nstderr-no-final-newline")...)
	if got := stderr.Bytes(); !bytes.Equal(got, wantStderr) {
		t.Fatalf("stderr bytes = %v, want %v", got, wantStderr)
	}
	if !stdout.hasWrite([]byte{0xce}) || !stdout.hasWrite([]byte{0xb2}) {
		t.Fatalf("UTF-8 bytes were not emitted in separate writes: %v", stdout.writes)
	}
}

func TestGenericWorker_TreeEmitsHierarchyAndReapsChildren(t *testing.T) {
	binaryName := "testcli"
	if runtime.GOOS == "windows" {
		binaryName += ".exe"
	}
	binaryPath := filepath.Join(t.TempDir(), binaryName)
	build := exec.Command("go", "build", "-o", binaryPath, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build testcli: %v\n%s", err, output)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(
		ctx,
		binaryPath,
		"generic-worker",
		"--mode", "tree",
		"--depth", "2",
		"--hold-ms", "20",
	)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		t.Fatalf("tree command: %v; stdout=%q stderr=%q", err, stdout.String(), stderr.String())
	}

	type treeEvent struct {
		Event     string `json:"event"`
		Level     int    `json:"level"`
		PID       int    `json:"pid"`
		ParentPID int    `json:"parent_pid"`
		Nodes     int    `json:"nodes"`
	}
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) != 4 {
		t.Fatalf("stdout lines = %d, want 4; stdout=%q", len(lines), stdout.String())
	}
	events := make([]treeEvent, 0, len(lines))
	for _, line := range lines {
		var event treeEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("decode tree event %q: %v", line, err)
		}
		events = append(events, event)
	}
	for level := 0; level <= 2; level++ {
		event := events[level]
		if event.Event != "tree.node" || event.Level != level || event.PID <= 0 {
			t.Fatalf("node %d = %+v, want level=%d with positive PID", level, event, level)
		}
		if level == 0 {
			if event.ParentPID != os.Getpid() {
				t.Fatalf("root parent PID = %d, want test PID %d", event.ParentPID, os.Getpid())
			}
		} else if event.ParentPID != events[level-1].PID {
			t.Fatalf("node %d parent PID = %d, want %d", level, event.ParentPID, events[level-1].PID)
		}
	}
	if complete := events[3]; complete.Event != "tree.complete" || complete.Nodes != 3 {
		t.Fatalf("complete event = %+v, want tree.complete with 3 nodes", complete)
	}
	if got := strings.Count(stderr.String(), "tree node level="); got != 3 {
		t.Fatalf("stderr node diagnostics = %d, want 3; stderr=%q", got, stderr.String())
	}
}

func TestGenericWorker_TreeRejectsUnboundedRequestsBeforeOutput(t *testing.T) {
	testCases := []struct {
		name string
		args []string
	}{
		{name: "negative depth", args: []string{"--mode", "tree", "--depth", "-1"}},
		{name: "excessive depth", args: []string{"--mode", "tree", "--depth", "9"}},
		{name: "negative hold", args: []string{"--mode", "tree", "--hold-ms", "-1"}},
		{name: "excessive hold", args: []string{"--mode", "tree", "--hold-ms", "10001"}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			stdout := &rejectingWriter{}
			var stderr bytes.Buffer

			exitCode := runGenericWorker(testCase.args, stdout, &stderr)

			if exitCode != 2 {
				t.Fatalf("exit code = %d, want 2; stderr=%q", exitCode, stderr.String())
			}
			if stdout.writes != 0 {
				t.Fatalf("stdout writes = %d, want 0 before bounds rejection", stdout.writes)
			}
			if !strings.Contains(stderr.String(), "tree bounds:") {
				t.Fatalf("stderr = %q, want tree bounds diagnostic", stderr.String())
			}
		})
	}
}

func TestGenericWorker_TreeRejectsForgedInternalInvocation(t *testing.T) {
	t.Setenv("AIMUX_TESTCLI_INTERNAL_TREE_NODE", "")
	testCases := []struct {
		name string
		args []string
	}{
		{name: "public tree with internal level", args: []string{"--mode", "tree", "--depth", "2", "--level", "2"}},
		{name: "tree node without internal marker", args: []string{"--mode", "tree-node", "--depth", "2", "--level", "1"}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			stdout := &rejectingWriter{}
			var stderr bytes.Buffer

			exitCode := runGenericWorker(testCase.args, stdout, &stderr)

			if exitCode != 2 {
				t.Fatalf("exit code = %d, want 2; stderr=%q", exitCode, stderr.String())
			}
			if stdout.writes != 0 {
				t.Fatalf("stdout writes = %d, want 0 before internal invocation rejection", stdout.writes)
			}
			if !strings.Contains(stderr.String(), "tree") {
				t.Fatalf("stderr = %q, want tree invocation diagnostic", stderr.String())
			}
		})
	}
}
