package pipe

import (
	"testing"
	"time"

	"github.com/thebtf/aimux/pkg/executor"
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
