package deps_test

import (
	"sync"
	"testing"

	"github.com/thebtf/aimux/pkg/loom/deps"
)

// TestUUIDGenerator_NonEmptyUnique verifies that successive calls produce
// non-empty, distinct IDs.
func TestUUIDGenerator_NonEmptyUnique(t *testing.T) {
	g := deps.UUIDGenerator()
	id1 := g.NewID()
	id2 := g.NewID()
	if id1 == "" {
		t.Fatal("UUIDGenerator returned empty ID")
	}
	if id1 == id2 {
		t.Errorf("UUIDGenerator returned duplicate IDs: %q", id1)
	}
}

// TestSequentialIDGenerator_Predictable verifies that IDs follow the expected
// "id-N" pattern starting from zero.
func TestSequentialIDGenerator_Predictable(t *testing.T) {
	g := deps.NewSequentialIDGenerator()
	want := []string{"id-0", "id-1", "id-2"}
	for _, w := range want {
		if got := g.NewID(); got != w {
			t.Errorf("SequentialIDGenerator.NewID() = %q; want %q", got, w)
		}
	}
}

// TestSequentialIDGenerator_ConcurrentSafe verifies that concurrent calls do not
// produce duplicate IDs (the atomic counter guards against races).
func TestSequentialIDGenerator_ConcurrentSafe(t *testing.T) {
	g := deps.NewSequentialIDGenerator()
	const n = 200
	ids := make([]string, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := range n {
		go func(i int) {
			defer wg.Done()
			ids[i] = g.NewID()
		}(i)
	}
	wg.Wait()

	seen := make(map[string]struct{}, n)
	for _, id := range ids {
		if _, dup := seen[id]; dup {
			t.Errorf("SequentialIDGenerator produced duplicate ID: %q", id)
		}
		seen[id] = struct{}{}
	}
}
