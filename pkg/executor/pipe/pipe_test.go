package pipe_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	osexec "os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/thebtf/aimux/pkg/executor/pipe"
	"github.com/thebtf/aimux/pkg/types"
)

var (
	genericWorkerBuildOnce  sync.Once
	genericWorkerBinaryPath string
	genericWorkerBuildDir   string
	genericWorkerBuildErr   error
	genericWorkerBuildOut   []byte
)

func TestMain(m *testing.M) {
	code := m.Run()
	if genericWorkerBuildDir != "" {
		_ = os.RemoveAll(genericWorkerBuildDir)
	}
	os.Exit(code)
}

func TestPipeExecutorSendEventsPreservesRawChannelBytes(t *testing.T) {
	binary := buildGenericWorkerTestCLI(t)
	var events []types.ExecutorEvent
	var eventsMu sync.Mutex
	resp, err := pipe.New().SendEvents(context.Background(), "raw", types.Message{Spawn: &types.SpawnArgs{
		Command: binary,
		Args:    []string{"generic-worker", "--mode", "framing"},
	}}, types.ExecutorEventSinkFunc(func(event types.ExecutorEvent) bool {
		eventsMu.Lock()
		events = append(events, event)
		eventsMu.Unlock()
		return true
	}))
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr []byte
	for _, event := range events {
		switch event.Channel {
		case "stdout":
			stdout = append(stdout, event.Content...)
		case "stderr":
			stderr = append(stderr, event.Content...)
		}
	}
	if !bytes.Contains(stdout, []byte{0xce, 0xb2}) || !bytes.Contains(stdout, []byte{0xff, 0xfe}) || !bytes.Contains(stdout, []byte{0x00, 0x1b}) || !bytes.HasSuffix(stdout, []byte("no-final-newline")) {
		t.Fatalf("stdout = %v; stderr = %v; response = %#v; events = %#v", stdout, stderr, resp, events)
	}
	if !bytes.Contains(stderr, []byte{0xff}) || !bytes.Contains(stderr, []byte{0x00}) || !bytes.HasSuffix(stderr, []byte("stderr-no-final-newline")) {
		t.Fatalf("stderr = %v", stderr)
	}
	if resp == nil || resp.ExitCode != 0 || resp.Stderr == "" {
		t.Fatalf("response = %#v", resp)
	}
}

func TestPipeExecutorSendEventsRejectedOutputTerminatesWithPartialEvidence(t *testing.T) {
	binary := buildGenericWorkerTestCLI(t)
	resp, err := pipe.New().SendEvents(context.Background(), "raw", types.Message{Spawn: &types.SpawnArgs{
		Command: binary,
		Args:    []string{"generic-worker", "--mode", "framing"},
	}}, types.ExecutorEventSinkFunc(func(types.ExecutorEvent) bool { return false }))
	if err != nil {
		t.Fatal(err)
	}
	if resp == nil || !resp.Partial {
		t.Fatalf("response = %#v, want bounded partial evidence after sink rejection", resp)
	}
}

func TestPipeExecutorSendEventsPreExitDrainDeadlineMarksPartial(t *testing.T) {
	binary := buildGenericWorkerTestCLI(t)
	resp, err := pipe.New().SendEvents(context.Background(), "pre-exit-drain", types.Message{Spawn: &types.SpawnArgs{
		Command: binary,
		Args: []string{
			"generic-worker",
			"--mode", "tree",
			"--depth", "1",
			"--hold-ms", "10000",
			"--root-exit",
		},
	}}, types.ExecutorEventSinkFunc(func(types.ExecutorEvent) bool { return true }))
	if err != nil {
		t.Fatal(err)
	}
	if resp == nil {
		t.Fatal("response = nil")
	}
	if resp.ExitCode != 0 {
		t.Fatalf("response = %#v, want clean root exit", resp)
	}
	if !strings.Contains(resp.Content, `"event":"tree.node"`) || strings.Contains(resp.Content, `"event":"tree.complete"`) {
		t.Fatalf("stdout = %q, want root/descendant evidence without natural tree completion", resp.Content)
	}
	if !resp.Partial {
		t.Fatalf("response = %#v, want Partial=true after ProcessManager forced descendant termination at the pre-exit drain deadline", resp)
	}
}

func TestPipeExecutorSendEventsPreservesNormativeFloodWithoutPrematureTruncation(t *testing.T) {
	binary := buildGenericWorkerTestCLI(t)
	started := time.Now()
	resp, err := pipe.New().SendEvents(context.Background(), "bounded-flood", types.Message{Spawn: &types.SpawnArgs{
		Command: binary,
		Args:    []string{"generic-worker", "--mode", "flood", "--count", "1024", "--chunk-bytes", "1024"},
	}}, types.ExecutorEventSinkFunc(func(types.ExecutorEvent) bool { return true }))
	if err != nil {
		t.Fatal(err)
	}
	if resp == nil || resp.Partial {
		if resp == nil {
			t.Fatal("response = nil; want complete output below the 8 MiB channel budget")
		}
		t.Fatalf("response partial = %v, stdout bytes = %d, stderr bytes = %d; want complete output below the 8 MiB channel budget", resp.Partial, len(resp.Content), len(resp.Stderr))
	}
	if len(resp.Content) != 1<<20 || len(resp.Stderr) != 1<<20 {
		t.Fatalf("response lengths = stdout %d, stderr %d; want 1 MiB each", len(resp.Content), len(resp.Stderr))
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("bounded flood took %s", elapsed)
	}
}

func buildGenericWorkerTestCLI(t *testing.T) string {
	t.Helper()
	genericWorkerBuildOnce.Do(func() {
		goMod, err := osexec.Command("go", "env", "GOMOD").Output()
		if err != nil {
			genericWorkerBuildErr = fmt.Errorf("resolve module root: %w", err)
			return
		}
		root := filepath.Dir(strings.TrimSpace(string(goMod)))
		genericWorkerBuildDir, err = os.MkdirTemp("", "aimux-pipe-testcli-")
		if err != nil {
			genericWorkerBuildErr = fmt.Errorf("create testcli temp dir: %w", err)
			return
		}
		genericWorkerBinaryPath = filepath.Join(genericWorkerBuildDir, "testcli")
		if runtime.GOOS == "windows" {
			genericWorkerBinaryPath += ".exe"
		}
		cmd := osexec.Command("go", "build", "-o", genericWorkerBinaryPath, "./cmd/testcli")
		cmd.Dir = root
		genericWorkerBuildOut, genericWorkerBuildErr = cmd.CombinedOutput()
	})
	if genericWorkerBuildErr != nil {
		t.Fatalf("build testcli: %v\n%s", genericWorkerBuildErr, genericWorkerBuildOut)
	}
	return genericWorkerBinaryPath
}

const (
	pipeCancelPartialOutputHelperEnv     = "AIMUX_PIPE_CANCEL_PARTIAL_OUTPUT_HELPER"
	pipeCancelPartialOutputHelperAddrEnv = "AIMUX_PIPE_CANCEL_PARTIAL_OUTPUT_HELPER_ADDR"
)

// TestPipeExecutorCancelPartialOutputHelper is re-executed in an isolated
// subprocess. It acknowledges only after line1 has been written to stdout, so
// the parent can cancel without assuming anything about process startup time.
func TestPipeExecutorCancelPartialOutputHelper(t *testing.T) {
	if os.Getenv(pipeCancelPartialOutputHelperEnv) != "1" {
		return
	}

	conn, err := net.Dial("tcp", os.Getenv(pipeCancelPartialOutputHelperAddrEnv))
	if err != nil {
		t.Fatalf("dial parent acknowledgement listener: %v", err)
	}
	defer conn.Close()

	if _, err := fmt.Fprintln(os.Stdout, "line1"); err != nil {
		t.Fatalf("write partial stdout: %v", err)
	}
	if _, err := conn.Write([]byte{1}); err != nil {
		t.Fatalf("acknowledge partial stdout write: %v", err)
	}

	// The parent holds the connection open until it cancels this process.
	_, _ = io.Copy(io.Discard, conn)
}

func echoCommand() (string, []string) {
	if runtime.GOOS == "windows" {
		return "cmd", []string{"/c", "echo", "hello world"}
	}
	return "echo", []string{"hello world"}
}

func sleepCommand() (string, []string) {
	if runtime.GOOS == "windows" {
		// ping -n 6 = ~5 seconds (1 per second + 1 initial)
		return "ping", []string{"-n", "6", "127.0.0.1"}
	}
	return "sleep", []string{"5"}
}

func TestPipeExecutor_Run_Echo(t *testing.T) {
	exec := pipe.New()

	if exec.Name() != "pipe" {
		t.Errorf("Name = %q, want pipe", exec.Name())
	}
	if !exec.Available() {
		t.Error("pipe executor should always be available")
	}

	cmd, args := echoCommand()

	result, err := exec.Run(context.Background(), types.SpawnArgs{
		Command: cmd,
		Args:    args,
	})

	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if result.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", result.ExitCode)
	}

	if !strings.Contains(result.Content, "hello world") {
		t.Errorf("Content = %q, want to contain 'hello world'", result.Content)
	}

	if result.DurationMS < 0 {
		t.Errorf("DurationMS = %d, want non-negative", result.DurationMS)
	}
}

func TestPipeExecutor_Run_Timeout(t *testing.T) {
	exec := pipe.New()

	cmd, args := sleepCommand()

	result, err := exec.Run(context.Background(), types.SpawnArgs{
		Command:        cmd,
		Args:           args,
		TimeoutSeconds: 1,
	})

	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if result.ExitCode != 124 {
		t.Errorf("ExitCode = %d, want 124 (timeout)", result.ExitCode)
	}

	if !result.Partial {
		t.Error("expected Partial=true for timeout")
	}

	if result.Error == nil {
		t.Error("expected Error to be set for timeout")
	}
}

func TestPipeExecutor_Run_ContextCancel(t *testing.T) {
	exec := pipe.New()

	ctx, cancel := context.WithCancel(context.Background())

	cmd, args := sleepCommand()

	go func() {
		time.Sleep(500 * time.Millisecond)
		cancel()
	}()

	result, err := exec.Run(ctx, types.SpawnArgs{
		Command: cmd,
		Args:    args,
	})

	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if result.ExitCode != 130 {
		t.Errorf("ExitCode = %d, want 130 (cancelled)", result.ExitCode)
	}

	if !result.Partial {
		t.Error("expected Partial=true for cancel")
	}
}

func TestPipeExecutor_Run_BadCommand(t *testing.T) {
	exec := pipe.New()

	_, err := exec.Run(context.Background(), types.SpawnArgs{
		Command: "nonexistent_command_xyz",
	})

	if err == nil {
		t.Fatal("expected error for nonexistent command")
	}

	if !types.IsTypedError(err, types.ErrorTypeExecutor) {
		t.Errorf("expected ExecutorError, got %T", err)
	}
}

func TestPipeSession_ProcessManagerTracking(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("uses cmd /c and ping -n — Windows-only syntax")
	}
	e := pipe.New()
	sess, err := e.Start(context.Background(), types.SpawnArgs{
		Command: "cmd",
		Args:    []string{"/c", "echo ready && ping -n 30 127.0.0.1 >nul"},
	})
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer sess.Close()

	// Process should be tracked and alive.
	if !sess.Alive() {
		t.Error("expected session to be alive")
	}
	if sess.PID() <= 0 {
		t.Errorf("expected PID > 0, got %d", sess.PID())
	}

	// Close should kill and cleanup.
	sess.Close()
	time.Sleep(100 * time.Millisecond)
	if sess.Alive() {
		t.Error("expected session to be dead after Close")
	}
}

func TestPipeSession_ShutdownKillsSession(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("uses cmd /c and ping -n — Windows-only syntax")
	}
	e := pipe.New()
	sess, err := e.Start(context.Background(), types.SpawnArgs{
		Command: "cmd",
		Args:    []string{"/c", "ping -n 30 127.0.0.1 >nul"},
	})
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	pid := sess.PID()
	if pid <= 0 {
		t.Fatal("expected PID > 0")
	}

	// Shutdown should kill all tracked sessions.
	t.Logf("before shutdown: alive=%v pid=%d", sess.Alive(), sess.PID())
	pipe.SessionProcessManager().Shutdown()
	t.Logf("after shutdown: alive=%v", sess.Alive())

	// Verify — after synchronous Shutdown+Kill, process must be dead.
	if sess.Alive() {
		t.Error("expected session to be dead after Shutdown")
	}
}

func TestPipeExecutor_Run_CancelReturnsPartialOutput(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for child acknowledgement: %v", err)
	}
	defer listener.Close()
	if tcpListener, ok := listener.(*net.TCPListener); ok {
		if err := tcpListener.SetDeadline(time.Now().Add(30 * time.Second)); err != nil {
			t.Fatalf("set acknowledgement listener deadline: %v", err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type runOutcome struct {
		result *types.Result
		err    error
	}
	outcomeCh := make(chan runOutcome, 1)
	go func() {
		result, runErr := pipe.New().Run(ctx, types.SpawnArgs{
			Command: os.Args[0],
			Args: []string{
				"-test.run=^TestPipeExecutorCancelPartialOutputHelper$",
				"-test.count=1",
			},
			Env: map[string]string{
				pipeCancelPartialOutputHelperEnv:     "1",
				pipeCancelPartialOutputHelperAddrEnv: listener.Addr().String(),
			},
			TimeoutSeconds: 30,
		})
		outcomeCh <- runOutcome{result: result, err: runErr}
	}()

	conn, err := listener.Accept()
	if err != nil {
		t.Fatalf("accept child acknowledgement: %v", err)
	}
	defer conn.Close()
	if err := conn.SetReadDeadline(time.Now().Add(30 * time.Second)); err != nil {
		t.Fatalf("set child acknowledgement deadline: %v", err)
	}
	ack := []byte{0}
	if _, err := io.ReadFull(conn, ack); err != nil {
		t.Fatalf("read child acknowledgement: %v", err)
	}
	cancel()

	var outcome runOutcome
	select {
	case outcome = <-outcomeCh:
	case <-time.After(30 * time.Second):
		t.Fatal("pipe executor did not return after cancellation")
	}
	if outcome.err != nil {
		t.Fatalf("expected partial result, got error: %v", outcome.err)
	}
	if outcome.result == nil {
		t.Fatal("expected partial result, got nil")
	}
	if !outcome.result.Partial {
		t.Error("expected Partial=true")
	}
	if outcome.result.ExitCode != 130 {
		t.Errorf("ExitCode = %d, want 130", outcome.result.ExitCode)
	}
	if !strings.Contains(outcome.result.Content, "line1") {
		t.Errorf("expected content to contain confirmed stdout write, got %q", outcome.result.Content)
	}
}
