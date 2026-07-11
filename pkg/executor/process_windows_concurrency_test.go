//go:build windows

package executor_test

import (
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"sync"
	"testing"
	"time"
	"unsafe"

	"github.com/thebtf/aimux/pkg/executor"
	"golang.org/x/sys/windows"
)

const (
	windowsConcurrentStartHelperEnv  = "AIMUX_WINDOWS_CONCURRENT_START_HELPER"
	windowsConcurrentStartMarkerEnv  = "AIMUX_WINDOWS_CONCURRENT_START_MARKER"
	windowsConcurrentStartReleaseEnv = "AIMUX_WINDOWS_CONCURRENT_START_RELEASE"
)

var getProcessHandleCount = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetProcessHandleCount")

func TestWindowsProcessTreeConcurrentOwnedStartHelper(t *testing.T) {
	if os.Getenv(windowsConcurrentStartHelperEnv) != "1" {
		return
	}
	markerPath := os.Getenv(windowsConcurrentStartMarkerEnv)
	if markerPath == "" {
		t.Fatal("concurrent-start helper marker path is blank")
	}
	if err := os.WriteFile(markerPath, []byte("started"), 0o600); err != nil {
		t.Fatalf("write concurrent-start marker: %v", err)
	}

	conn, err := net.Dial("tcp", os.Getenv(windowsConcurrentStartReleaseEnv))
	if err != nil {
		t.Fatalf("dial concurrent-start release listener: %v", err)
	}
	defer conn.Close()
	if _, err := fmt.Fprintln(conn, os.Getpid()); err != nil {
		t.Fatalf("acknowledge concurrent-start helper: %v", err)
	}
	_, _ = io.Copy(io.Discard, conn)
}

func TestWindowsProcessTree_ConcurrentOwnedStartsRemainBounded(t *testing.T) {
	const (
		baselineSamples = 3
		cohortSize      = 8
		stressSize      = 24
	)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for concurrent-start helpers: %v", err)
	}
	defer listener.Close()
	if tcpListener, ok := listener.(*net.TCPListener); ok {
		if err := tcpListener.SetDeadline(time.Now().Add(90 * time.Second)); err != nil {
			t.Fatalf("set concurrent-start listener deadline: %v", err)
		}
	}

	processManager := executor.NewProcessManager()
	fixtureDir := t.TempDir()
	baseline := make([]time.Duration, 0, baselineSamples)
	for i := range baselineSamples {
		markerPath := filepath.Join(fixtureDir, fmt.Sprintf("baseline-%d", i))
		handle, elapsed, spawnErr := spawnWindowsConcurrentStartHelper(processManager, listener, markerPath)
		if spawnErr != nil {
			t.Fatalf("spawn baseline helper %d: %v", i, spawnErr)
		}
		conn := acceptWindowsConcurrentStartHelper(t, listener)
		if _, statErr := os.Stat(markerPath); statErr != nil {
			_ = conn.Close()
			processManager.Kill(handle)
			processManager.Cleanup(handle)
			t.Fatalf("baseline helper %d did not execute marker: %v", i, statErr)
		}
		_ = conn.Close()
		processManager.Kill(handle)
		processManager.Cleanup(handle)
		baseline = append(baseline, elapsed)
	}
	sort.Slice(baseline, func(i, j int) bool { return baseline[i] < baseline[j] })
	baselineMedian := baseline[len(baseline)/2]

	cohortElapsed, latencies := runWindowsConcurrentStartMarkerCohort(
		t,
		processManager,
		listener,
		fixtureDir,
		"cohort",
		cohortSize,
	)
	limit := 4*baselineMedian + time.Second
	t.Logf("baseline=%v median=%s cohort=%s latencies=%v limit=%s", baseline, baselineMedian, cohortElapsed, latencies, limit)
	if cohortElapsed > limit {
		t.Fatalf("%d concurrent owned starts took %s, want <= %s (4x single-start median plus 1s)", cohortSize, cohortElapsed, limit)
	}

	stressElapsed, stressLatencies := runWindowsConcurrentStartMarkerCohort(
		t,
		processManager,
		listener,
		fixtureDir,
		"stress",
		stressSize,
	)
	stressLimit := 12*baselineMedian + 2*time.Second
	t.Logf("stress=%s latencies=%v limit=%s", stressElapsed, stressLatencies, stressLimit)
	if stressElapsed > stressLimit {
		t.Fatalf(
			"%d concurrent owned starts took %s, want <= %s (12x single-start median plus 2s)",
			stressSize,
			stressElapsed,
			stressLimit,
		)
	}
}

func runWindowsConcurrentStartMarkerCohort(
	t *testing.T,
	processManager *executor.ProcessManager,
	listener net.Listener,
	fixtureDir string,
	prefix string,
	cohortSize int,
) (time.Duration, []time.Duration) {
	t.Helper()
	type spawnOutcome struct {
		index   int
		handle  *executor.ProcessHandle
		elapsed time.Duration
		err     error
	}
	startGate := make(chan struct{})
	outcomes := make(chan spawnOutcome, cohortSize)
	var workers sync.WaitGroup
	workers.Add(cohortSize)
	for i := range cohortSize {
		go func(index int) {
			defer workers.Done()
			<-startGate
			markerPath := filepath.Join(fixtureDir, fmt.Sprintf("%s-%d", prefix, index))
			handle, elapsed, spawnErr := spawnWindowsConcurrentStartHelper(processManager, listener, markerPath)
			outcomes <- spawnOutcome{index: index, handle: handle, elapsed: elapsed, err: spawnErr}
		}(i)
	}

	cohortStarted := time.Now()
	close(startGate)
	workers.Wait()
	close(outcomes)
	cohortElapsed := time.Since(cohortStarted)

	handles := make([]*executor.ProcessHandle, 0, cohortSize)
	latencies := make([]time.Duration, cohortSize)
	defer func() {
		for _, handle := range handles {
			processManager.Kill(handle)
			processManager.Cleanup(handle)
		}
	}()
	firstErrorIndex := -1
	var firstSpawnError error
	for outcome := range outcomes {
		if outcome.err != nil {
			if firstSpawnError == nil {
				firstErrorIndex = outcome.index
				firstSpawnError = outcome.err
			}
			continue
		}
		handles = append(handles, outcome.handle)
		latencies[outcome.index] = outcome.elapsed
	}
	if firstSpawnError != nil {
		t.Fatalf("spawn cohort helper %d: %v", firstErrorIndex, firstSpawnError)
	}

	connections := make([]net.Conn, 0, cohortSize)
	defer func() {
		for _, conn := range connections {
			_ = conn.Close()
		}
	}()
	for range cohortSize {
		connections = append(connections, acceptWindowsConcurrentStartHelper(t, listener))
	}
	for i := range cohortSize {
		markerPath := filepath.Join(fixtureDir, fmt.Sprintf("%s-%d", prefix, i))
		if _, statErr := os.Stat(markerPath); statErr != nil {
			t.Errorf("%s helper %d did not execute marker: %v", prefix, i, statErr)
		}
	}
	return cohortElapsed, latencies
}

func TestWindowsProcessTree_ConcurrentOwnedStartsDoNotLeakHandles(t *testing.T) {
	const (
		cohortSize       = 8
		maxWarmupCohorts = 12
		stableSamples    = 2
		measuredCohorts  = 3
		tolerance        = 8
	)
	processManager := executor.NewProcessManager()

	// Blocking Windows process waits cause the Go runtime to grow its reusable
	// thread pool over the first few cohorts. Establish a steady-state baseline
	// before measuring retained Job/process/thread/snapshot handles.
	previous := currentWindowsProcessHandleCount(t)
	stable := 0
	warmups := 0
	for warmups < maxWarmupCohorts && stable < stableSamples {
		runWindowsOwnedExitCohort(t, processManager, cohortSize)
		runtime.GC()
		time.Sleep(25 * time.Millisecond)
		current := currentWindowsProcessHandleCount(t)
		if current <= previous+1 {
			stable++
		} else {
			stable = 0
		}
		previous = current
		warmups++
	}
	if stable < stableSamples {
		t.Fatalf("current-process handle count did not stabilize after %d warm-up cohorts", warmups)
	}
	before := previous

	for range measuredCohorts {
		runWindowsOwnedExitCohort(t, processManager, cohortSize)
	}
	runtime.GC()
	time.Sleep(25 * time.Millisecond)
	after := currentWindowsProcessHandleCount(t)
	t.Logf("current-process handles before=%d after=%d tolerance=%d", before, after, tolerance)
	if after > before+tolerance {
		t.Fatalf(
			"current-process handles grew from %d to %d after %d owned starts; want increase <= %d",
			before,
			after,
			cohortSize*measuredCohorts,
			tolerance,
		)
	}
}

func runWindowsOwnedExitCohort(t *testing.T, processManager *executor.ProcessManager, cohortSize int) {
	t.Helper()
	type outcome struct {
		index  int
		handle *executor.ProcessHandle
		err    error
	}
	startGate := make(chan struct{})
	outcomes := make(chan outcome, cohortSize)
	var workers sync.WaitGroup
	workers.Add(cohortSize)
	for i := range cohortSize {
		go func(index int) {
			defer workers.Done()
			<-startGate
			handle, err := processManager.Spawn(exec.Command("cmd", "/c", "exit", "0"))
			outcomes <- outcome{index: index, handle: handle, err: err}
		}(i)
	}
	close(startGate)
	workers.Wait()
	close(outcomes)

	handles := make([]*executor.ProcessHandle, 0, cohortSize)
	firstErrorIndex := -1
	var firstError error
	for result := range outcomes {
		if result.err != nil {
			if firstError == nil {
				firstErrorIndex = result.index
				firstError = result.err
			}
			continue
		}
		handles = append(handles, result.handle)
	}
	for i, handle := range handles {
		select {
		case waitErr := <-handle.Done:
			if waitErr != nil && firstError == nil {
				firstErrorIndex = i
				firstError = waitErr
			}
		case <-time.After(10 * time.Second):
			processManager.Kill(handle)
			if firstError == nil {
				firstErrorIndex = i
				firstError = fmt.Errorf("timed out waiting for owned exit helper")
			}
		}
		processManager.Cleanup(handle)
	}
	if firstError != nil {
		t.Fatalf("owned exit helper %d failed: %v", firstErrorIndex, firstError)
	}
}

func currentWindowsProcessHandleCount(t *testing.T) uint32 {
	t.Helper()
	var count uint32
	result, _, callErr := getProcessHandleCount.Call(
		uintptr(windows.CurrentProcess()),
		uintptr(unsafe.Pointer(&count)),
	)
	if result == 0 {
		t.Fatalf("GetProcessHandleCount: %v", callErr)
	}
	return count
}

func spawnWindowsConcurrentStartHelper(
	processManager *executor.ProcessManager,
	listener net.Listener,
	markerPath string,
) (*executor.ProcessHandle, time.Duration, error) {
	command := newWindowsConcurrentStartHelperCommand(listener, markerPath)
	started := time.Now()
	handle, err := processManager.Spawn(command)
	return handle, time.Since(started), err
}

func newWindowsConcurrentStartHelperCommand(listener net.Listener, markerPath string) *exec.Cmd {
	command := exec.Command(
		os.Args[0],
		"-test.run=^TestWindowsProcessTreeConcurrentOwnedStartHelper$",
		"-test.count=1",
	)
	command.Env = append(
		os.Environ(),
		windowsConcurrentStartHelperEnv+"=1",
		windowsConcurrentStartMarkerEnv+"="+markerPath,
		windowsConcurrentStartReleaseEnv+"="+listener.Addr().String(),
	)
	return command
}

func acceptWindowsConcurrentStartHelper(t *testing.T, listener net.Listener) net.Conn {
	t.Helper()
	conn, err := listener.Accept()
	if err != nil {
		t.Fatalf("accept concurrent-start helper: %v", err)
	}
	return conn
}
