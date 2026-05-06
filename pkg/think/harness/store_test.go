package harness

import (
	"context"
	"errors"
	"testing"
)

func TestInMemoryStoreCreateGetAndUnknownSession(t *testing.T) {
	store := NewInMemoryStore()
	session := NewThinkingSession("s1", validFrame(t))

	created, err := store.Create(context.Background(), session)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.ID != "s1" {
		t.Fatalf("created ID = %q, want s1", created.ID)
	}

	got, err := store.Get(context.Background(), "s1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Frame.Task != session.Frame.Task {
		t.Fatalf("got task = %q, want %q", got.Frame.Task, session.Frame.Task)
	}

	if _, err := store.Get(context.Background(), "missing"); !errors.Is(err, ErrUnknownSession) {
		t.Fatalf("unknown session error = %v, want ErrUnknownSession", err)
	}
}

func TestInMemoryStoreCopyOnWriteUpdate(t *testing.T) {
	store := NewInMemoryStore()
	session := NewThinkingSession("s1", validFrame(t))
	if _, err := store.Create(context.Background(), session); err != nil {
		t.Fatalf("create: %v", err)
	}

	first, err := store.Update(context.Background(), "s1", func(current ThinkingSession) (ThinkingSession, error) {
		return current.ApplyPatch(KnowledgePatch{
			LedgerAdds: KnowledgeLedger{
				Known: []LedgerEntry{{ID: "k1", Text: "first fact"}},
			},
		})
	})
	if err != nil {
		t.Fatalf("first update: %v", err)
	}

	second, err := store.Update(context.Background(), "s1", func(current ThinkingSession) (ThinkingSession, error) {
		return current.ApplyPatch(KnowledgePatch{
			LedgerAdds: KnowledgeLedger{
				Unknown: []LedgerEntry{{ID: "u1", Text: "second question"}},
			},
		})
	})
	if err != nil {
		t.Fatalf("second update: %v", err)
	}

	if len(first.Ledger.Unknown) != 0 {
		t.Fatalf("first snapshot mutated: %+v", first.Ledger.Unknown)
	}
	if len(second.Ledger.Known) != 1 || len(second.Ledger.Unknown) != 1 {
		t.Fatalf("second ledger = %+v, want one known and one unknown", second.Ledger)
	}

	got, err := store.Get(context.Background(), "s1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	got.Ledger.Known[0].Text = "mutated snapshot"

	again, err := store.Get(context.Background(), "s1")
	if err != nil {
		t.Fatalf("get again: %v", err)
	}
	if again.Ledger.Known[0].Text != "first fact" {
		t.Fatalf("store returned mutable snapshot: %+v", again.Ledger.Known[0])
	}
}

func TestInMemoryStoreConcurrentSessionIsolation(t *testing.T) {
	store := NewInMemoryStore()
	for _, id := range []string{"a", "b"} {
		if _, err := store.Create(context.Background(), NewThinkingSession(id, validFrame(t))); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}

	if _, err := store.Update(context.Background(), "a", func(current ThinkingSession) (ThinkingSession, error) {
		return current.ApplyPatch(KnowledgePatch{
			LedgerAdds: KnowledgeLedger{Known: []LedgerEntry{{ID: "ka", Text: "only session a"}}},
		})
	}); err != nil {
		t.Fatalf("update a: %v", err)
	}

	a, err := store.Get(context.Background(), "a")
	if err != nil {
		t.Fatalf("get a: %v", err)
	}
	b, err := store.Get(context.Background(), "b")
	if err != nil {
		t.Fatalf("get b: %v", err)
	}
	if len(a.Ledger.Known) != 1 {
		t.Fatalf("session a known entries = %d, want 1", len(a.Ledger.Known))
	}
	if len(b.Ledger.Known) != 0 {
		t.Fatalf("session b was contaminated: %+v", b.Ledger.Known)
	}
}
