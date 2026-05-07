package codex

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// TestResumer_FindBySearchTerm exercises the Resumer.FindBySearchTerm method
// using the fakeAppServerDialer infrastructure.
func TestResumer_FindBySearchTerm(t *testing.T) {
	t.Run("found_first_match", func(t *testing.T) {
		d := newFakeDialer(t)
		threadID := "thread-abc123"
		d.respondWith("thread/list", ThreadListResponse{
			Data: []ThreadSummary{
				{ID: threadID, Preview: "fix the bug", CWD: "/tmp/proj"},
			},
		})

		proc := newTestProcess(t, d)
		pool := newTestPool(t, nil)
		pool.mu.Lock()
		pool.entries["proj-1"] = readyEntry(proc)
		pool.mu.Unlock()

		r := NewResumer(pool.CodexPool)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		got, err := r.FindBySearchTerm(ctx, "proj-1", "/tmp/proj", "fix the bug")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != threadID {
			t.Errorf("got %q, want %q", got, threadID)
		}
	})

	t.Run("empty_result_no_error", func(t *testing.T) {
		d := newFakeDialer(t)
		d.respondWith("thread/list", ThreadListResponse{Data: nil})

		proc := newTestProcess(t, d)
		pool := newTestPool(t, nil)
		pool.mu.Lock()
		pool.entries["proj-2"] = readyEntry(proc)
		pool.mu.Unlock()

		r := NewResumer(pool.CodexPool)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		got, err := r.FindBySearchTerm(ctx, "proj-2", "/tmp", "nonexistent")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "" {
			t.Errorf("expected empty string, got %q", got)
		}
	})

	t.Run("rpc_error_propagated", func(t *testing.T) {
		d := newFakeDialer(t)
		d.respondError("thread/list", -32001, "internal error")

		proc := newTestProcess(t, d)
		pool := newTestPool(t, nil)
		pool.mu.Lock()
		pool.entries["proj-3"] = readyEntry(proc)
		pool.mu.Unlock()

		r := NewResumer(pool.CodexPool)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_, err := r.FindBySearchTerm(ctx, "proj-3", "/tmp", "query")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})
}

// TestResumer_Resume exercises the Resumer.Resume method.
func TestResumer_Resume(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		d := newFakeDialer(t)
		thread := Thread{ID: "thread-xyz", CWD: "/proj"}
		d.respondWith("thread/resume", ThreadResumeResponse{Thread: thread})

		proc := newTestProcess(t, d)
		pool := newTestPool(t, nil)
		pool.mu.Lock()
		pool.entries["proj-r1"] = readyEntry(proc)
		pool.mu.Unlock()

		r := NewResumer(pool.CodexPool)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		sandbox, _ := ForClass(JobClassTask)
		got, err := r.Resume(ctx, "proj-r1", "/proj", "thread-xyz", sandbox)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.ID != thread.ID {
			t.Errorf("got thread ID %q, want %q", got.ID, thread.ID)
		}
	})

	t.Run("thread_not_found_sentinel", func(t *testing.T) {
		d := newFakeDialer(t)
		d.respondError("thread/resume", -32600, "thread not found")

		proc := newTestProcess(t, d)
		pool := newTestPool(t, nil)
		pool.mu.Lock()
		pool.entries["proj-r2"] = readyEntry(proc)
		pool.mu.Unlock()

		r := NewResumer(pool.CodexPool)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		sandbox, _ := ForClass(JobClassTask)
		_, err := r.Resume(ctx, "proj-r2", "/proj", "missing-thread", sandbox)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if err != ErrThreadNotFound {
			t.Errorf("expected ErrThreadNotFound sentinel, got: %v", err)
		}
	})
}

// TestThreadListParams_UseStateDbOnly verifies that UseStateDbOnly is always
// serialized as true (Fix 2 from post-impl-fixes.md).
func TestThreadListParams_UseStateDbOnly(t *testing.T) {
	p := ThreadListParams{
		SearchTerm:     "test",
		UseStateDbOnly: true,
	}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	v, ok := m["useStateDbOnly"]
	if !ok {
		t.Error("useStateDbOnly field missing from JSON")
	}
	if v != true {
		t.Errorf("useStateDbOnly should be true, got %v", v)
	}
}

// TestThreadListResponse_DataField verifies that thread/list response
// uses the "data" key (not "threads") — Fix 1 from post-impl-fixes.md.
func TestThreadListResponse_DataField(t *testing.T) {
	raw := `{"data": [{"id": "t1", "cwd": "/proj"}], "nextCursor": ""}`
	var resp ThreadListResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Data) != 1 {
		t.Fatalf("expected 1 thread in Data, got %d", len(resp.Data))
	}
	if resp.Data[0].ID != "t1" {
		t.Errorf("expected thread ID t1, got %q", resp.Data[0].ID)
	}
}

// TestThreadListResponse_EmptyData verifies that an empty data array is handled correctly.
func TestThreadListResponse_EmptyData(t *testing.T) {
	raw := `{"data": [], "nextCursor": ""}`
	var resp ThreadListResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Data) != 0 {
		t.Errorf("expected empty Data, got %d items", len(resp.Data))
	}
}
