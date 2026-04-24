package think

import (
	"testing"
)

func TestPushPattern_Basic(t *testing.T) {
	ClearSessions()
	defer ClearSessions()

	GetOrCreateSession("s1", "think", map[string]any{"key": "value"})

	ok := PushPattern("s1", "debugging_approach")
	if !ok {
		t.Fatal("PushPattern returned false unexpectedly")
	}

	if cur := CurrentPattern("s1"); cur != "debugging_approach" {
		t.Errorf("CurrentPattern = %q, want debugging_approach", cur)
	}
}

func TestPushPattern_MissingSession_ReturnsFalse(t *testing.T) {
	ClearSessions()
	defer ClearSessions()

	ok := PushPattern("nonexistent", "think")
	if ok {
		t.Error("PushPattern on nonexistent session should return false")
	}
}

func TestPopPattern_Empty_ReturnsEmpty(t *testing.T) {
	ClearSessions()
	defer ClearSessions()

	GetOrCreateSession("s1", "think", nil)

	popped := PopPattern("s1")
	if popped != "" {
		t.Errorf("PopPattern on empty stack: want \"\", got %q", popped)
	}
}

func TestPopPattern_MissingSession_ReturnsEmpty(t *testing.T) {
	ClearSessions()
	defer ClearSessions()

	popped := PopPattern("nonexistent")
	if popped != "" {
		t.Errorf("PopPattern on nonexistent session: want \"\", got %q", popped)
	}
}

func TestPushPop_RestoresState(t *testing.T) {
	ClearSessions()
	defer ClearSessions()

	GetOrCreateSession("s1", "think", map[string]any{"baseKey": "baseValue"})
	PushPattern("s1", "debugging_approach")

	// Modify state after push.
	UpdateSessionState("s1", map[string]any{"newKey": "newValue"})

	popped := PopPattern("s1")
	if popped != "debugging_approach" {
		t.Errorf("PopPattern: want debugging_approach, got %q", popped)
	}

	sess := GetSession("s1")
	if _, ok := sess.State["newKey"]; ok {
		t.Error("state after pop should not contain key added after push")
	}
	if sess.State["baseKey"] != "baseValue" {
		t.Errorf("baseKey after pop = %v, want baseValue", sess.State["baseKey"])
	}
}

func TestCurrentPattern_EmptyStack_ReturnsBasePattern(t *testing.T) {
	ClearSessions()
	defer ClearSessions()

	GetOrCreateSession("s1", "think", nil)

	cur := CurrentPattern("s1")
	if cur != "think" {
		t.Errorf("CurrentPattern (empty stack) = %q, want think", cur)
	}
}

func TestCurrentPattern_MissingSession_ReturnsEmpty(t *testing.T) {
	ClearSessions()
	defer ClearSessions()

	cur := CurrentPattern("nonexistent")
	if cur != "" {
		t.Errorf("CurrentPattern (missing session) = %q, want \"\"", cur)
	}
}

func TestPushPattern_MaxDepth(t *testing.T) {
	ClearSessions()
	defer ClearSessions()

	GetOrCreateSession("s1", "think", nil)

	for i := 0; i < maxPatternStackDepth; i++ {
		ok := PushPattern("s1", "debugging_approach")
		if !ok {
			t.Fatalf("PushPattern at depth %d returned false unexpectedly", i)
		}
	}

	// Next push should fail.
	ok := PushPattern("s1", "scientific_method")
	if ok {
		t.Error("PushPattern beyond max depth should return false")
	}
}

func TestPushPop_MultiLevel(t *testing.T) {
	ClearSessions()
	defer ClearSessions()

	GetOrCreateSession("s1", "think", nil)

	PushPattern("s1", "A")
	PushPattern("s1", "B")
	PushPattern("s1", "C")

	if cur := CurrentPattern("s1"); cur != "C" {
		t.Errorf("CurrentPattern after 3 pushes = %q, want C", cur)
	}

	p := PopPattern("s1")
	if p != "C" {
		t.Errorf("first pop = %q, want C", p)
	}
	if cur := CurrentPattern("s1"); cur != "B" {
		t.Errorf("CurrentPattern after 1 pop = %q, want B", cur)
	}

	p = PopPattern("s1")
	if p != "B" {
		t.Errorf("second pop = %q, want B", p)
	}
	if cur := CurrentPattern("s1"); cur != "A" {
		t.Errorf("CurrentPattern after 2 pops = %q, want A", cur)
	}

	p = PopPattern("s1")
	if p != "A" {
		t.Errorf("third pop = %q, want A", p)
	}
	if cur := CurrentPattern("s1"); cur != "think" {
		t.Errorf("CurrentPattern after all pops = %q, want think", cur)
	}
}
