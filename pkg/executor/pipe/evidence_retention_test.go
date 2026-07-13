package pipe

import (
	"context"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/thebtf/aimux/pkg/executor"
	"github.com/thebtf/aimux/pkg/swarm"
	"github.com/thebtf/aimux/pkg/types"
)

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
	e.HoldProcessEvidence(pinned)
	for i := 0; i < processEvidenceLimit; i++ {
		id := types.ExecutionID(string(rune('a' + i)))
		if i == 0 {
			id = pinned
		}
		if err := e.retainProcessEvidence(id, &executor.ProcessHandle{PID: i + 1, StartedAt: time.Unix(0, int64(i+1))}); err != nil {
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
	e.ReleaseProcessEvidence(pinned)
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
	var wg sync.WaitGroup
	for i := 0; i < processEvidenceLimit*2; i++ {
		id := types.ExecutionID("reservation-" + string(rune(i)))
		wg.Add(1)
		go func() {
			defer wg.Done()
			if e.HoldProcessEvidence(id) {
				acquired.Add(1)
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
		e.ReleaseProcessEvidence(types.ExecutionID("reservation-" + string(rune(i))))
	}
	if !e.HoldProcessEvidence("reusable") {
		t.Fatal("released reservation capacity was not reusable")
	}
	e.ReleaseProcessEvidence("reusable")
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
	if !e.HoldProcessEvidence("reusable-after-spawn-failure") {
		t.Fatal("spawn failure reservation capacity was not reusable")
	}
	e.ReleaseProcessEvidence("reusable-after-spawn-failure")
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
