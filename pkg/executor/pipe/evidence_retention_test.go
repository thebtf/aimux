package pipe

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/thebtf/aimux/pkg/executor"
	"github.com/thebtf/aimux/pkg/swarm"
	"github.com/thebtf/aimux/pkg/types"
)

const processEvidenceLeaseHelperEnv = "AIMUX_PIPE_PROCESS_EVIDENCE_LEASE_HELPER"
const processEvidenceLeaseFileEnv = "AIMUX_PIPE_PROCESS_EVIDENCE_LEASE_FILE"
const processEvidenceLeasePersistentHelperEnv = "AIMUX_PIPE_PROCESS_EVIDENCE_LEASE_PERSISTENT_HELPER"

func TestProcessEvidenceLeaseSideEffectHelper(t *testing.T) {
	if os.Getenv(processEvidenceLeaseHelperEnv) != "1" {
		return
	}
	if err := os.WriteFile(os.Getenv(processEvidenceLeaseFileEnv), []byte("started"), 0o600); err != nil {
		os.Exit(2)
	}
	os.Exit(0)
}

func TestProcessEvidenceLeasePersistentHelper(t *testing.T) {
	if os.Getenv(processEvidenceLeasePersistentHelperEnv) != "1" {
		return
	}
	if err := os.WriteFile(os.Getenv(processEvidenceLeaseFileEnv), []byte("started"), 0o600); err != nil {
		os.Exit(2)
	}
	select {}
}

func processEvidenceLeaseMessage(file string) types.Message {
	return types.Message{Spawn: &types.SpawnArgs{
		Command: os.Args[0],
		Args:    []string{"-test.run=^TestProcessEvidenceLeaseSideEffectHelper$", "-test.count=1"},
		Env: map[string]string{
			processEvidenceLeaseHelperEnv: "1",
			processEvidenceLeaseFileEnv:   file,
		},
	}}
}

func processEvidenceLeasePersistentMessage(file string) types.Message {
	return types.Message{Spawn: &types.SpawnArgs{
		Command: os.Args[0],
		Args:    []string{"-test.run=^TestProcessEvidenceLeasePersistentHelper$", "-test.count=1"},
		Env: map[string]string{
			processEvidenceLeasePersistentHelperEnv: "1",
			processEvidenceLeaseFileEnv:             file,
		},
	}}
}

func TestProcessEvidenceLeaseRejectsUnownedAndInvalidConsumersBeforeSideEffects(t *testing.T) {
	e := New()
	file := t.TempDir() + string(os.PathSeparator) + "started"
	lease, _, ok := e.AcquireProcessEvidenceLease("owned")
	if !ok {
		t.Fatal("acquire lease")
	}
	var stdinCalls atomic.Int32
	e.stdinPipe = func(*exec.Cmd) (io.WriteCloser, error) {
		stdinCalls.Add(1)
		return nil, errors.New("unexpected stdin setup")
	}
	direct := processEvidenceLeaseMessage(file)
	direct.Spawn.Stdin = "attacker"
	if _, err := e.SendEvents(context.Background(), "owned", direct, nil); err == nil {
		t.Fatal("unowned direct caller consumed held lease")
	}
	if stdinCalls.Load() != 0 {
		t.Fatalf("unowned caller reached stdin setup %d times", stdinCalls.Load())
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatalf("unowned caller performed side effect: %v", err)
	}
	if _, err := e.SendEventsWithProcessEvidenceLease(context.Background(), "owned", struct{}{}, processEvidenceLeaseMessage(file), nil); err == nil {
		t.Fatal("wrong lease consumed reservation")
	}
	if _, err := e.SendEventsWithProcessEvidenceLease(context.Background(), "owned", lease, processEvidenceLeaseMessage(file), nil); err != nil {
		t.Fatalf("owned execution: %v", err)
	}
	if _, err := os.Stat(file); err != nil {
		t.Fatalf("owned execution did not start helper: %v", err)
	}
	if _, err := e.SendEventsWithProcessEvidenceLease(context.Background(), "owned", lease, processEvidenceLeaseMessage(file), nil); err == nil {
		t.Fatal("consumed lease started a second process")
	}
	e.ReleaseProcessEvidenceLease("owned", lease)
}

func TestProcessEvidenceLeaseAbortClosesFutureAndOldReleaseCannotTouchNewGeneration(t *testing.T) {
	e := New()
	old, _, ok := e.AcquireProcessEvidenceLease("same")
	if !ok {
		t.Fatal("acquire old lease")
	}
	e.ReleaseProcessEvidenceLease("same", old)
	newLease, ready, ok := e.AcquireProcessEvidenceLease("same")
	if !ok {
		t.Fatal("acquire new lease")
	}
	e.ReleaseProcessEvidenceLease("same", old)
	if _, err := e.SendEvents(context.Background(), "same", processEvidenceLeaseMessage(t.TempDir()+string(os.PathSeparator)+"stale"), nil); err == nil {
		t.Fatal("old release removed newer lease")
	}
	e.stdinPipe = func(*exec.Cmd) (io.WriteCloser, error) { return nil, errors.New("stdin failed") }
	if _, err := e.SendEventsWithProcessEvidenceLease(context.Background(), "same", newLease, types.Message{Spawn: &types.SpawnArgs{Command: os.Args[0], Stdin: "x"}}, nil); err == nil {
		t.Fatal("stdin setup failure unexpectedly succeeded")
	}
	if _, open := <-ready; open {
		t.Fatal("aborted lease published process evidence")
	}
	if _, _, ok := e.AcquireProcessEvidenceLease("same"); !ok {
		t.Fatal("aborted lease did not reclaim capacity")
	}
}

func TestProcessEvidenceLeaseCancelledBeforeSetupClosesFutureAndReusesCapacity(t *testing.T) {
	e := New()
	lease, ready, ok := e.AcquireProcessEvidenceLease("cancelled-before-setup")
	if !ok {
		t.Fatal("acquire lease")
	}
	var stdinCalls atomic.Int32
	e.stdinPipe = func(*exec.Cmd) (io.WriteCloser, error) {
		stdinCalls.Add(1)
		return nil, errors.New("stdin should not be reached")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	file := t.TempDir() + string(os.PathSeparator) + "started"
	message := processEvidenceLeaseMessage(file)
	message.Spawn.Stdin = "must not reach stdin"
	if _, err := e.SendEventsWithProcessEvidenceLease(ctx, "cancelled-before-setup", lease, message, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled leased send = %v, want context.Canceled", err)
	}
	if stdinCalls.Load() != 0 {
		t.Fatalf("cancelled lease reached stdin setup %d times", stdinCalls.Load())
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatalf("cancelled lease started helper: %v", err)
	}
	if _, open := <-ready; open {
		t.Fatal("cancelled lease left evidence future open")
	}
	if reusable, _, ok := e.AcquireProcessEvidenceLease("cancelled-before-setup"); !ok {
		t.Fatal("cancelled lease did not reclaim capacity")
	} else {
		e.ReleaseProcessEvidenceLease("cancelled-before-setup", reusable)
	}
}

func TestProcessEvidenceLeaseCancelBetweenCheckAndSpawnClosesFutureAndReusesCapacity(t *testing.T) {
	e := New()
	const id = types.ExecutionID("cancel-between-check-and-spawn")
	lease, ready, ok := e.AcquireProcessEvidenceLease(id)
	if !ok {
		t.Fatal("acquire lease")
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	_, stdin, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stdin.Close() })
	e.stdinPipe = func(*exec.Cmd) (io.WriteCloser, error) {
		close(entered) // proves the manual context check already passed.
		<-release
		return stdin, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	file := t.TempDir() + string(os.PathSeparator) + "started"
	message := processEvidenceLeaseMessage(file)
	message.Spawn.Stdin = "non-empty stdin reaches the seam"
	type outcome struct {
		response *types.Response
		err      error
	}
	outcomes := make(chan outcome, 1)
	go func() {
		response, err := e.SendEventsWithProcessEvidenceLease(ctx, id, lease, message, nil)
		outcomes <- outcome{response, err}
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("stdin seam was not reached")
	}
	cancel()
	close(release)
	var got outcome
	select {
	case got = <-outcomes:
	case <-time.After(time.Second):
		t.Fatal("cancelled send did not return")
	}
	if !errors.Is(got.err, context.Canceled) || !types.IsTypedError(got.err, types.ErrorTypeExecutor) {
		t.Errorf("cancelled send = response %#v, err %v; want cancellation-shaped executor error", got.response, got.err)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatalf("cancelled send started helper: %v", err)
	}
	if _, open := <-ready; open {
		t.Fatal("cancelled send left evidence future open")
	}
	if reusable, _, ok := e.AcquireProcessEvidenceLease(id); !ok {
		t.Fatal("cancelled send did not reclaim capacity")
	} else {
		e.ReleaseProcessEvidenceLease(id, reusable)
	}
}

func TestProcessEvidenceLeaseCommitFailureClosesFuture(t *testing.T) {
	e := New()
	file := t.TempDir() + string(os.PathSeparator) + "started"
	var captured *executor.ProcessHandle
	e.commitEvidence = func(h *executor.ProcessHandle) error {
		captured = h
		deadline := time.Now().Add(time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(file); err == nil {
				if !executor.SharedPM.IsAlive(h) {
					return errors.New("persistent helper exited before attachment failure")
				}
				return errors.New("attach failed")
			}
			time.Sleep(10 * time.Millisecond)
		}
		return errors.New("persistent helper did not start before attachment failure")
	}
	t.Cleanup(func() {
		if executor.SharedPM.IsAlive(captured) {
			executor.SharedPM.Kill(captured)
		}
		executor.SharedPM.Cleanup(captured)
	})
	lease, ready, ok := e.AcquireProcessEvidenceLease("attach-failure")
	if !ok {
		t.Fatal("acquire lease")
	}
	if _, err := e.SendEventsWithProcessEvidenceLease(context.Background(), "attach-failure", lease, processEvidenceLeasePersistentMessage(file), nil); err == nil {
		t.Fatal("commit failure unexpectedly succeeded")
	}
	if captured == nil {
		t.Fatal("attachment failure did not capture spawned handle")
	}
	if executor.SharedPM.IsAlive(captured) {
		t.Fatal("attachment failure left persistent root alive")
	}
	if !captured.TreeStopped() {
		t.Fatal("attachment failure did not stop owned process tree")
	}
	select {
	case <-captured.Done:
	default:
		t.Fatal("attachment failure returned before process reaped")
	}
	if _, open := <-ready; open {
		t.Fatal("commit failure left evidence future open")
	}
	if _, _, ok := e.AcquireProcessEvidenceLease("attach-failure"); !ok {
		t.Fatal("commit failure did not reclaim capacity")
	}
}

func TestProcessEvidenceRetentionNeverEvictsLiveRecords(t *testing.T) {
	e := New()
	for i := 0; i < processEvidenceLimit; i++ {
		id := types.ExecutionID(string(rune('a' + i)))
		if err := e.retainProcessEvidence(id, &executor.ProcessHandle{PID: i + 1, StartedAt: time.Unix(0, int64(i+1))}); err != nil {
			t.Fatalf("retain %d: %v", i, err)
		}
	}
	if err := e.retainProcessEvidence("overflow", &executor.ProcessHandle{PID: 999, StartedAt: time.Now()}); err == nil {
		t.Fatal("live capacity overflow succeeded")
	}
	if _, err := e.ProcessTreeEvidence(nil, "b"); err != nil {
		t.Fatalf("live evidence was evicted: %v", err)
	}
	e.evidenceMu.Lock()
	record := e.evidence["a"]
	record.tree.Stopped = true
	record.final = true
	e.evidence["a"] = record
	e.evidenceMu.Unlock()
	if err := e.retainProcessEvidence("replacement", &executor.ProcessHandle{PID: 1000, StartedAt: time.Now()}); err != nil {
		t.Fatalf("stopped entry was not evictable: %v", err)
	}
	if _, err := e.ProcessTreeEvidence(nil, "a"); err == nil {
		t.Fatal("stopped oldest evidence was retained over replacement")
	}
	if err := e.retainProcessEvidence("replacement", &executor.ProcessHandle{PID: 1001, StartedAt: time.Now()}); err == nil {
		t.Fatal("duplicate execution evidence succeeded")
	}
}

func TestProcessEvidenceRetentionEvictsFinalUnconfirmedRecords(t *testing.T) {
	e := New()
	for i := 0; i < processEvidenceLimit; i++ {
		id := types.ExecutionID(string(rune('a' + i)))
		if err := e.retainProcessEvidence(id, &executor.ProcessHandle{PID: i + 1, StartedAt: time.Unix(0, int64(i+1))}); err != nil {
			t.Fatalf("retain %d: %v", i, err)
		}
		e.evidenceMu.Lock()
		record := e.evidence[id]
		record.final = true
		e.evidence[id] = record
		e.evidenceMu.Unlock()
	}
	if err := e.retainProcessEvidence("replacement", &executor.ProcessHandle{PID: 999, StartedAt: time.Now()}); err != nil {
		t.Fatalf("final unconfirmed capacity stayed exhausted: %v", err)
	}
	if _, err := e.ProcessTreeEvidence(nil, "a"); err == nil {
		t.Fatal("oldest final-unconfirmed evidence was not reclaimed")
	}
	for i := 1; i < processEvidenceLimit; i++ {
		evidence, err := e.ProcessTreeEvidence(nil, types.ExecutionID(string(rune('a'+i))))
		if err != nil || evidence.Stopped {
			t.Fatalf("final unconfirmed evidence %d = %#v, %v", i, evidence, err)
		}
	}
}

func TestProcessEvidenceRetentionHoldsFinalSnapshotUntilReleased(t *testing.T) {
	e := New()
	const pinned = types.ExecutionID("pinned")
	pinnedLease, _, ok := e.AcquireProcessEvidenceLease(pinned)
	if !ok {
		t.Fatal("acquire pinned lease")
	}
	pinnedToken, err := e.consumeProcessEvidenceLease(pinned, pinnedLease)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < processEvidenceLimit; i++ {
		id := types.ExecutionID(string(rune('a' + i)))
		lease := processEvidenceLease{}
		if i == 0 {
			id = pinned
			lease = pinnedToken
		} else if err := e.reserveProcessEvidence(id); err != nil {
			t.Fatalf("reserve %d: %v", i, err)
		}
		if err := e.commitProcessEvidenceReservation(id, &executor.ProcessHandle{PID: i + 1, StartedAt: time.Unix(0, int64(i+1))}, lease); err != nil {
			t.Fatalf("retain %d: %v", i, err)
		}
		e.finalizeProcessEvidence(id)
	}
	if err := e.retainProcessEvidence("while-held", &executor.ProcessHandle{PID: 999, StartedAt: time.Now()}); err != nil {
		t.Fatalf("final evidence evicted before its Swarm handoff: %v", err)
	}
	if _, err := e.ProcessTreeEvidence(nil, pinned); err != nil {
		t.Fatalf("held final evidence was evicted: %v", err)
	}
	e.ReleaseProcessEvidenceLease(pinned, pinnedLease)
	if err := e.retainProcessEvidence("after-release", &executor.ProcessHandle{PID: 1000, StartedAt: time.Now()}); err != nil {
		t.Fatalf("released final evidence was not reclaimable: %v", err)
	}
	if _, err := e.ProcessTreeEvidence(nil, pinned); err == nil {
		t.Fatal("released final evidence was retained over replacement")
	}
}

func TestProcessTreeEvidenceDoesNotTreatRootExitAsManagedTreeStop(t *testing.T) {
	e := New()
	h := &executor.ProcessHandle{PID: 17, StartedAt: time.Now()}
	h.MarkExited()
	if err := e.retainProcessEvidence("root-exited", h); err != nil {
		t.Fatal(err)
	}
	evidence, err := e.ProcessTreeEvidence(nil, "root-exited")
	if err != nil {
		t.Fatal(err)
	}
	if evidence.Stopped {
		t.Fatalf("root exit fabricated whole-tree stop evidence: %#v", evidence)
	}
}

func TestProcessEvidenceReservationsAreHardBoundedAndReusable(t *testing.T) {
	e := New()
	var acquired atomic.Int32
	var leases sync.Map
	var wg sync.WaitGroup
	for i := 0; i < processEvidenceLimit*2; i++ {
		id := types.ExecutionID("reservation-" + string(rune(i)))
		wg.Add(1)
		go func() {
			defer wg.Done()
			if lease, _, ok := e.AcquireProcessEvidenceLease(id); ok {
				acquired.Add(1)
				leases.Store(id, lease)
			}
		}()
	}
	wg.Wait()
	if got := acquired.Load(); got != processEvidenceLimit {
		t.Fatalf("concurrent acquired reservations = %d, want %d", got, processEvidenceLimit)
	}
	e.evidenceMu.Lock()
	reserved := len(e.reservations)
	e.evidenceMu.Unlock()
	if reserved != processEvidenceLimit {
		t.Fatalf("retained reservations = %d, want %d", reserved, processEvidenceLimit)
	}
	for i := 0; i < processEvidenceLimit*2; i++ {
		id := types.ExecutionID("reservation-" + string(rune(i)))
		if lease, ok := leases.Load(id); ok {
			e.ReleaseProcessEvidenceLease(id, lease)
		}
	}
	lease, _, ok := e.AcquireProcessEvidenceLease("reusable")
	if !ok {
		t.Fatal("released reservation capacity was not reusable")
	}
	e.ReleaseProcessEvidenceLease("reusable", lease)
}

func TestSendEventsSpawnFailureRollsBackReservation(t *testing.T) {
	e := New()
	if _, err := e.SendEvents(context.Background(), "spawn-failure", types.Message{Spawn: &types.SpawnArgs{Command: "aimux-command-that-does-not-exist"}}, nil); err == nil {
		t.Fatal("spawn failure unexpectedly succeeded")
	}
	e.evidenceMu.Lock()
	reserved := len(e.reservations)
	e.evidenceMu.Unlock()
	if reserved != 0 {
		t.Fatalf("spawn failure left %d reservations", reserved)
	}
	lease, _, ok := e.AcquireProcessEvidenceLease("reusable-after-spawn-failure")
	if !ok {
		t.Fatal("spawn failure reservation capacity was not reusable")
	}
	e.ReleaseProcessEvidenceLease("reusable-after-spawn-failure", lease)
}

func TestSwarmNaturalRootExitWithUnconfirmedPipeTreeFails(t *testing.T) {
	e := New()
	e.finalEvidence = func(evidence types.ProcessTreeEvidence) types.ProcessTreeEvidence {
		evidence.Stopped = false
		return evidence
	}
	s := swarm.New(func(string) (types.ExecutorV2, error) {
		return executor.NewCLIPipeAdapter(e), nil
	}, nil)
	h, err := s.Get(context.Background(), "unconfirmed-pipe", swarm.Stateful, swarm.WithScope("scope"))
	if err != nil {
		t.Fatal(err)
	}
	command, args := "sh", []string{"-c", "exit 0"}
	if runtime.GOOS == "windows" {
		command, args = "cmd", []string{"/c", "exit", "0"}
	}
	terminal := make(chan types.ExecutorEvent, 1)
	_, err = s.Execute(context.Background(), h, "scope", "unconfirmed-pipe", types.Message{Spawn: &types.SpawnArgs{Command: command, Args: args}}, types.ExecutorEventSinkFunc(func(event types.ExecutorEvent) bool {
		if event.Terminal {
			terminal <- event
		}
		return true
	}))
	if err != nil {
		t.Fatal(err)
	}
	if event := <-terminal; event.Type == "completed" {
		t.Fatalf("terminal = %#v, want fail-closed result for unconfirmed exact pipe tree", event)
	}
}
