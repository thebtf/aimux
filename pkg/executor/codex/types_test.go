package codex

import (
	"encoding/json"
	"testing"
)

func TestJSONRPCRequest_Marshal(t *testing.T) {
	req := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      42,
		Method:  "thread/start",
	}
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	var back JSONRPCRequest
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if back.ID != 42 || back.Method != "thread/start" {
		t.Errorf("round-trip mismatch: %+v", back)
	}
}

func TestJSONRPCResponse_Unmarshal(t *testing.T) {
	raw := `{"jsonrpc":"2.0","id":1,"result":{"thread":{"id":"thr-abc","ephemeral":false}}}`
	var resp JSONRPCResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.ID != 1 {
		t.Errorf("expected id=1, got %d", resp.ID)
	}
	// Unmarshal result into ThreadStartResponse.
	var tsr ThreadStartResponse
	if err := json.Unmarshal(resp.Result, &tsr); err != nil {
		t.Fatal(err)
	}
	if tsr.Thread.ID != "thr-abc" {
		t.Errorf("expected thread.id=thr-abc, got %q", tsr.Thread.ID)
	}
}

func TestThreadStartResponse_VerifiedFieldPath(t *testing.T) {
	// VERIFIED: codex 0.128.0 returns result.thread.id (not result.threadId).
	raw := `{"thread":{"id":"thr-xyz","cwd":"/some/dir","ephemeral":false}}`
	var tsr ThreadStartResponse
	if err := json.Unmarshal([]byte(raw), &tsr); err != nil {
		t.Fatal(err)
	}
	if tsr.Thread.ID != "thr-xyz" {
		t.Errorf("expected thread.id=thr-xyz, got %q", tsr.Thread.ID)
	}
}

func TestTurnStartResponse_VerifiedFieldPath(t *testing.T) {
	// VERIFIED: codex 0.128.0 returns result.turn.id (not result.turnId).
	raw := `{"turn":{"id":"turn-123","status":"running"}}`
	var tsr TurnStartResponse
	if err := json.Unmarshal([]byte(raw), &tsr); err != nil {
		t.Fatal(err)
	}
	if tsr.Turn.ID != "turn-123" {
		t.Errorf("expected turn.id=turn-123, got %q", tsr.Turn.ID)
	}
}

func TestItemCompletedNotification_AgentMessage(t *testing.T) {
	// VERIFIED: item.text carries agent message when item.type=="agentMessage".
	raw := `{"item":{"type":"agentMessage","id":"msg-1","text":"Hello from codex"},"threadId":"thr-abc","turnId":"turn-123"}`
	var n ItemCompletedNotification
	if err := json.Unmarshal([]byte(raw), &n); err != nil {
		t.Fatal(err)
	}
	if n.Item.Type != "agentMessage" {
		t.Errorf("expected type=agentMessage, got %q", n.Item.Type)
	}
	if n.Item.Text != "Hello from codex" {
		t.Errorf("expected text, got %q", n.Item.Text)
	}
	if n.ThreadID != "thr-abc" {
		t.Errorf("expected threadId=thr-abc, got %q", n.ThreadID)
	}
}

func TestCodexTaskMeta_RoundTrip(t *testing.T) {
	meta := CodexTaskMeta{
		ThreadID:  "thr-abc",
		TurnID:    "turn-123",
		JobClass:  "review",
	}
	// Simulate Loom metadata storage: marshal to map[string]any.
	b, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if m["thread_id"] != "thr-abc" {
		t.Errorf("expected thread_id, got %v", m["thread_id"])
	}
	// Round-trip back.
	b2, _ := json.Marshal(m)
	var back CodexTaskMeta
	if err := json.Unmarshal(b2, &back); err != nil {
		t.Fatal(err)
	}
	if back.ThreadID != meta.ThreadID {
		t.Errorf("ThreadID mismatch: %q vs %q", back.ThreadID, meta.ThreadID)
	}
}

func TestInboundMessage_Classification(t *testing.T) {
	t.Run("response", func(t *testing.T) {
		raw := `{"jsonrpc":"2.0","id":5,"result":{}}`
		var m inboundMessage
		json.Unmarshal([]byte(raw), &m)
		if !m.isResponse() {
			t.Error("expected isResponse")
		}
	})
	t.Run("notification", func(t *testing.T) {
		raw := `{"jsonrpc":"2.0","method":"item/completed","params":{}}`
		var m inboundMessage
		json.Unmarshal([]byte(raw), &m)
		if !m.isNotification() {
			t.Error("expected isNotification")
		}
	})
	t.Run("server_request", func(t *testing.T) {
		raw := `{"jsonrpc":"2.0","id":3,"method":"sampling/request","params":{}}`
		var m inboundMessage
		json.Unmarshal([]byte(raw), &m)
		if !m.isServerRequest() {
			t.Error("expected isServerRequest")
		}
	})
}
