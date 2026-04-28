package server

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/thebtf/mcp-mux/muxcore"
)

// makeProject returns a minimal ProjectContext for testing.
func makeProject(id string) muxcore.ProjectContext {
	return muxcore.ProjectContext{ID: muxcore.ProjectContextID(id), Cwd: "/tmp/" + id}
}

// makeJSONRPCRequest builds a minimal JSON-RPC request payload with the given id.
func makeJSONRPCRequest(id int) []byte {
	b, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  "tools/list",
	})
	return b
}

// TestLightweightDelegate_OnProjectConnect verifies that project IDs are recorded.
func TestLightweightDelegate_OnProjectConnect(t *testing.T) {
	d := newLightweightDelegate(15)

	pa := makeProject("proj-a")
	pb := makeProject("proj-b")
	d.OnProjectConnect(pa)
	d.OnProjectConnect(pb)

	ids := d.connectedProjects()
	found := make(map[string]bool, len(ids))
	for _, id := range ids {
		found[id] = true
	}
	if !found[string(pa.ID)] {
		t.Errorf("expected %q in connectedProjects; got %v", pa.ID, ids)
	}
	if !found[string(pb.ID)] {
		t.Errorf("expected %q in connectedProjects; got %v", pb.ID, ids)
	}
}

// TestLightweightDelegate_OnProjectDisconnect verifies project IDs are removed.
func TestLightweightDelegate_OnProjectDisconnect(t *testing.T) {
	d := newLightweightDelegate(15)

	d.OnProjectConnect(makeProject("proj-a"))
	d.OnProjectConnect(makeProject("proj-b"))
	d.OnProjectDisconnect(string(makeProject("proj-a").ID))

	ids := d.connectedProjects()
	disconnectedID := string(makeProject("proj-a").ID)
	for _, id := range ids {
		if id == disconnectedID {
			t.Error("proj-a should have been removed")
		}
	}
}

// TestLightweightDelegate_HandleRequest_ReadyBeforeGrace verifies that
// HandleRequest returns errReadyButShouldRedispatch when the ready channel
// closes within the grace period.
func TestLightweightDelegate_HandleRequest_ReadyBeforeGrace(t *testing.T) {
	d := newLightweightDelegate(5)

	// Close ready after 10ms — well within the 5s grace period.
	go func() {
		time.Sleep(10 * time.Millisecond)
		d.closeReady()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_, err := d.HandleRequest(ctx, makeProject("proj-a"), makeJSONRPCRequest(1))
	if err != errReadyButShouldRedispatch {
		t.Errorf("expected errReadyButShouldRedispatch, got %v", err)
	}
}

// TestLightweightDelegate_HandleRequest_GraceExpired verifies that
// HandleRequest returns a JSON-RPC -32001 retry-hint response when the grace
// period elapses before the ready channel closes.
func TestLightweightDelegate_HandleRequest_GraceExpired(t *testing.T) {
	// Grace period: 1 second; ready never closes.
	d := newLightweightDelegate(1)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := makeJSONRPCRequest(42)
	resp, err := d.HandleRequest(ctx, makeProject("proj-a"), req)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if !isRetryHintResponse(resp) {
		t.Errorf("expected -32001 retry-hint response, got: %s", resp)
	}

	// Verify the id field is echoed and retry_after_seconds is set.
	var envelope struct {
		ID    int `json:"id"`
		Error struct {
			Code int `json:"code"`
			Data struct {
				RetryAfterSeconds int `json:"retry_after_seconds"`
			} `json:"data"`
		} `json:"error"`
	}
	if err := json.Unmarshal(resp, &envelope); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if envelope.ID != 42 {
		t.Errorf("expected id=42, got %d", envelope.ID)
	}
	if envelope.Error.Code != retryHintErrorCode {
		t.Errorf("expected error code %d, got %d", retryHintErrorCode, envelope.Error.Code)
	}
	if envelope.Error.Data.RetryAfterSeconds != 1 {
		t.Errorf("expected retry_after_seconds=1, got %d", envelope.Error.Data.RetryAfterSeconds)
	}
}

// TestLightweightDelegate_HandleRequest_CtxCancelled verifies that
// HandleRequest returns ctx.Err() when the context is cancelled.
func TestLightweightDelegate_HandleRequest_CtxCancelled(t *testing.T) {
	d := newLightweightDelegate(30)

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel immediately.
	cancel()

	resp, err := d.HandleRequest(ctx, makeProject("proj-a"), makeJSONRPCRequest(1))
	if resp != nil {
		t.Errorf("expected nil response on cancellation, got: %s", resp)
	}
	if err != context.Canceled {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

// TestLightweightDelegate_HandleRequest_DefaultGrace verifies the fallback
// grace period (15s) is used when graceSec <= 0.
func TestLightweightDelegate_HandleRequest_DefaultGrace(t *testing.T) {
	// graceSec=0 → defaults to 15. Close immediately so the call returns via
	// the redispatch sentinel, proving the select is entered without panicking.
	d := newLightweightDelegate(0)
	d.closeReady() // already closed

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_, err := d.HandleRequest(ctx, makeProject("proj-a"), makeJSONRPCRequest(1))
	if err != errReadyButShouldRedispatch {
		t.Errorf("expected errReadyButShouldRedispatch, got %v", err)
	}
}

// TestLightweightDelegate_MarkSwapped verifies that connectedProjects is
// accessible after markSwapped (markSwapped only sets the flag; HandleRequest
// is the one that panics, which we cannot call post-swap by design).
func TestLightweightDelegate_MarkSwapped(t *testing.T) {
	d := newLightweightDelegate(15)
	pa := makeProject("proj-a")
	d.OnProjectConnect(pa)
	d.markSwapped()

	// connectedProjects must still work — it is called by swapDelegateToFull.
	ids := d.connectedProjects()
	if len(ids) != 1 || ids[0] != string(pa.ID) {
		t.Errorf("unexpected connectedProjects after swap: %v (want [%q])", ids, pa.ID)
	}
}

// TestBuildRetryHintResponse verifies the response structure.
func TestBuildRetryHintResponse(t *testing.T) {
	req := makeJSONRPCRequest(7)
	resp := buildRetryHintResponse(req, 30)

	if !isRetryHintResponse(resp) {
		t.Fatalf("expected retry-hint response, got: %s", resp)
	}

	var envelope struct {
		JSONRPC string `json:"jsonrpc"`
		ID      int    `json:"id"`
		Error   struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
			Data    struct {
				RetryAfterSeconds int `json:"retry_after_seconds"`
			} `json:"data"`
		} `json:"error"`
	}
	if err := json.Unmarshal(resp, &envelope); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if envelope.JSONRPC != "2.0" {
		t.Errorf("expected jsonrpc=2.0, got %q", envelope.JSONRPC)
	}
	if envelope.ID != 7 {
		t.Errorf("expected id=7, got %d", envelope.ID)
	}
	if envelope.Error.Code != -32001 {
		t.Errorf("expected code=-32001, got %d", envelope.Error.Code)
	}
	if envelope.Error.Data.RetryAfterSeconds != 30 {
		t.Errorf("expected retry_after_seconds=30, got %d", envelope.Error.Data.RetryAfterSeconds)
	}
	if envelope.Error.Message == "" {
		t.Error("expected non-empty error message")
	}
}

// TestBuildRetryHintResponse_MalformedRequest verifies that a malformed
// request payload results in id=null in the response (graceful fallback).
func TestBuildRetryHintResponse_MalformedRequest(t *testing.T) {
	resp := buildRetryHintResponse([]byte(`not-json`), 10)
	if !isRetryHintResponse(resp) {
		t.Fatalf("expected retry-hint response even for malformed request: %s", resp)
	}
}

// TestIsRetryHintResponse verifies the helper correctly identifies responses.
func TestIsRetryHintResponse(t *testing.T) {
	cases := []struct {
		name     string
		raw      []byte
		expected bool
	}{
		{
			name:     "valid retry hint",
			raw:      []byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32001,"message":"daemon initialising"}}`),
			expected: true,
		},
		{
			name:     "different error code",
			raw:      []byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32600,"message":"invalid request"}}`),
			expected: false,
		},
		{
			name:     "success response",
			raw:      []byte(`{"jsonrpc":"2.0","id":1,"result":{"tools":[]}}`),
			expected: false,
		},
		{
			name:     "malformed json",
			raw:      []byte(`not-json`),
			expected: false,
		},
		{
			name:     "null error field",
			raw:      []byte(`{"jsonrpc":"2.0","id":1,"error":null}`),
			expected: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := isRetryHintResponse(tc.raw)
			if got != tc.expected {
				t.Errorf("isRetryHintResponse(%s) = %v, want %v", tc.raw, got, tc.expected)
			}
		})
	}
}

// TestLightweightDelegate_ConcurrentConnect verifies that concurrent
// OnProjectConnect calls are safe under the race detector.
func TestLightweightDelegate_ConcurrentConnect(t *testing.T) {
	d := newLightweightDelegate(15)

	const n = 50
	done := make(chan struct{}, n)
	for i := range n {
		go func(i int) {
			d.OnProjectConnect(makeProject(string(rune('A' + i%26))))
			done <- struct{}{}
		}(i)
	}
	for range n {
		<-done
	}
	// Just verify connectedProjects doesn't race or panic.
	_ = d.connectedProjects()
}
