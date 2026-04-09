package ratelimit

import (
	"testing"
	"time"
)

func TestAllow_UnderLimit(t *testing.T) {
	l := New(10, 20)
	// 5 calls, all should succeed (well under burst of 20)
	for i := 0; i < 5; i++ {
		if !l.Allow("tool_a") {
			t.Fatalf("call %d: expected Allow=true, got false", i+1)
		}
	}
}

func TestAllow_OverBurst(t *testing.T) {
	burst := 5
	l := New(1, burst) // slow refill so burst+1 hits the limit
	// First `burst` calls should all succeed.
	for i := 0; i < burst; i++ {
		if !l.Allow("tool_b") {
			t.Fatalf("call %d: expected Allow=true, got false", i+1)
		}
	}
	// The burst+1 call must be rejected.
	if l.Allow("tool_b") {
		t.Fatal("expected Allow=false for burst+1 call, got true")
	}
}

func TestAllow_Replenish(t *testing.T) {
	// 1 rps, burst of 1 — exhaust, wait >1 s, then allow again.
	l := New(1, 1)
	if !l.Allow("tool_c") {
		t.Fatal("first call should succeed")
	}
	if l.Allow("tool_c") {
		t.Fatal("second immediate call should be rejected")
	}
	// Wait long enough to replenish at least one token.
	time.Sleep(1100 * time.Millisecond)
	if !l.Allow("tool_c") {
		t.Fatal("call after replenish should succeed")
	}
}

func TestAllow_PerTool(t *testing.T) {
	burst := 3
	l := New(1, burst)

	// Exhaust tool_x
	for i := 0; i < burst; i++ {
		l.Allow("tool_x")
	}
	if l.Allow("tool_x") {
		t.Fatal("tool_x should be exhausted")
	}

	// tool_y should have a fresh bucket
	if !l.Allow("tool_y") {
		t.Fatal("tool_y should still be allowed — independent bucket")
	}
}
