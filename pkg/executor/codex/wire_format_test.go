package codex

// wire_format_test.go — Schema-snapshot tests for codex protocol param types.
//
// PURPOSE: Catch missing JSON fields (schema drift) before runtime.
// v5.10.0 shipped a codex executor that failed on first call with
// "missing field clientInfo in initialize". All 65+ unit tests at the time
// mocked JSONLClient, so none verified that Go struct JSON output actually
// matched the codex wire protocol schema. These tests close that gap.
//
// DESIGN: Each test JSON-marshals a param struct as production code does,
// re-parses into map[string]any, and asserts required keys exist.
// Required fields come from .agent/codex-types-generated/(v2/)TypeName.ts.
// Fields marked `?` in TS are optional — only non-optional fields are asserted.
//
// All tests pass on master as of v5.10.0 (PR #170 added clientInfo and
// experimentalApi to the structs). The comment block below is preserved as
// historical context for the regression these tests were written to catch.

import (
	"encoding/json"
	"testing"
)

// requiredKeys verifies that JSON marshal output of params contains every key
// in required. Fails the test with a clear message for each missing key,
// printing the full payload to aid debugging.
func requiredKeys(t *testing.T, params any, required []string) {
	t.Helper()
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("json.Marshal(%T): %v", params, err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("json.Unmarshal round-trip: %v", err)
	}
	for _, key := range required {
		if _, ok := got[key]; !ok {
			t.Errorf("missing required JSON key %q in %T\npayload: %s", key, params, raw)
		}
	}
}

// noUnexpectedKeys optionally checks for keys that must NOT appear in the wire
// output (e.g. internal struct fields accidentally exported with json tags).
// Pass an empty slice to skip this check.
func noUnexpectedKeys(t *testing.T, params any, forbidden []string) {
	t.Helper()
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("json.Marshal(%T): %v", params, err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("json.Unmarshal round-trip: %v", err)
	}
	for _, key := range forbidden {
		if _, ok := got[key]; ok {
			t.Errorf("unexpected internal key %q appeared in wire output for %T\npayload: %s", key, params, raw)
		}
	}
}

// ──────────────────────────────────────────────────────────────
// InitializeParams (root, not v2)
// TS: { clientInfo: ClientInfo, capabilities: InitializeCapabilities | null }
// clientInfo is REQUIRED (no ?) — this is the field whose absence broke v5.10.0.
// ──────────────────────────────────────────────────────────────

// TestWireFormat_InitializeParams_RequiresClientInfo verifies clientInfo is
// present in the wire payload. This was the missing field that broke v5.10.0.
func TestWireFormat_InitializeParams_RequiresClientInfo(t *testing.T) {
	p := InitializeParams{
		Capabilities: InitializeCapabilities{
			OptOutNotificationMethods: OptOutNotificationMethods,
		},
	}
	// TS schema: { clientInfo: ClientInfo, capabilities: ... }
	// clientInfo is non-optional (no ?).
	requiredKeys(t, p, []string{"clientInfo", "capabilities"})
}

// TestWireFormat_InitializeParams_NoInternalLeakage verifies no unexpected
// internal Go fields end up in the wire payload.
func TestWireFormat_InitializeParams_NoInternalLeakage(t *testing.T) {
	p := InitializeParams{}
	noUnexpectedKeys(t, p, []string{"Capabilities"})
}

// ──────────────────────────────────────────────────────────────
// InitializeCapabilities
// TS: { experimentalApi: boolean, optOutNotificationMethods?: ... }
// experimentalApi is REQUIRED (no ?).
// ──────────────────────────────────────────────────────────────

// TestWireFormat_InitializeCapabilities_RequiresExperimentalApi verifies
// experimentalApi appears in the wire payload (added in PR #170).
func TestWireFormat_InitializeCapabilities_RequiresExperimentalApi(t *testing.T) {
	c := InitializeCapabilities{
		OptOutNotificationMethods: OptOutNotificationMethods,
	}
	// TS schema: { experimentalApi: boolean, optOutNotificationMethods?: Array<string> | null }
	requiredKeys(t, c, []string{"experimentalApi"})
}

// ──────────────────────────────────────────────────────────────
// ThreadStartParams (v2/ThreadStartParams.ts)
// All fields are optional (marked ?), so no required-key assertions apply.
// We test round-trip fidelity and check that optional fields that are set
// do appear in the JSON output with the correct key names.
// ──────────────────────────────────────────────────────────────

func TestWireFormat_ThreadStartParams_FieldNames(t *testing.T) {
	p := ThreadStartParams{
		Model:          "codex-mini-latest",
		CWD:            "/tmp/project",
		ApprovalPolicy: AskForApprovalNever,
		Sandbox:        SandboxModeDangerFullAccess,
		Ephemeral:      true,
	}
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// TS key names (camelCase) must match exactly.
	wantPresent := map[string]any{
		"model":          "codex-mini-latest",
		"cwd":            "/tmp/project",
		"approvalPolicy": string(AskForApprovalNever),
		"sandbox":        string(SandboxModeDangerFullAccess),
		"ephemeral":      true,
	}
	for key, want := range wantPresent {
		got, ok := got[key]
		if !ok {
			t.Errorf("ThreadStartParams: missing key %q in wire output", key)
			continue
		}
		switch wantVal := want.(type) {
		case string:
			if gotStr, ok := got.(string); !ok || gotStr != wantVal {
				t.Errorf("ThreadStartParams[%q]: got %v, want %q", key, got, wantVal)
			}
		case bool:
			if gotBool, ok := got.(bool); !ok || gotBool != wantVal {
				t.Errorf("ThreadStartParams[%q]: got %v, want %v", key, got, wantVal)
			}
		}
	}
}

// ──────────────────────────────────────────────────────────────
// ThreadResumeParams (v2/ThreadResumeParams.ts)
// Required: threadId (non-optional, no ?)
// ──────────────────────────────────────────────────────────────

func TestWireFormat_ThreadResumeParams_RequiresThreadId(t *testing.T) {
	p := ThreadResumeParams{
		ThreadID: "thr-abc",
	}
	// TS: { threadId: string, ... all others optional }
	requiredKeys(t, p, []string{"threadId"})
}

func TestWireFormat_ThreadResumeParams_FieldNames(t *testing.T) {
	p := ThreadResumeParams{
		ThreadID:     "thr-abc",
		CWD:          "/tmp/proj",
		ExcludeTurns: true,
	}
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("json.Marshal(%T): %v", p, err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("json.Unmarshal round-trip: %v", err)
	}
	for _, key := range []string{"threadId", "cwd", "excludeTurns"} {
		if _, ok := got[key]; !ok {
			t.Errorf("ThreadResumeParams: missing key %q in wire output\npayload: %s", key, raw)
		}
	}
}

// ──────────────────────────────────────────────────────────────
// ThreadListParams (v2/ThreadListParams.ts)
// All fields optional in the TS schema, but useStateDbOnly has a critical
// behavioral note: ALWAYS true (270x speedup). We verify the key name.
// ──────────────────────────────────────────────────────────────

func TestWireFormat_ThreadListParams_UseStateDbOnlyKeyName(t *testing.T) {
	p := ThreadListParams{
		UseStateDbOnly: true,
		Limit:          10,
	}
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// TS key: useStateDbOnly (camelCase, matches Go tag).
	if v, ok := got["useStateDbOnly"]; !ok {
		t.Errorf("ThreadListParams: missing key useStateDbOnly in wire output\npayload: %s", raw)
	} else if v != true {
		t.Errorf("ThreadListParams[useStateDbOnly]: got %v, want true", v)
	}
}

func TestWireFormat_ThreadListParams_SearchTermKeyName(t *testing.T) {
	p := ThreadListParams{
		SearchTerm:     "my-project",
		UseStateDbOnly: true,
	}
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("json.Marshal(%T): %v", p, err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("json.Unmarshal round-trip: %v", err)
	}
	if _, ok := got["searchTerm"]; !ok {
		t.Errorf("ThreadListParams: missing key searchTerm\npayload: %s", raw)
	}
}

// ──────────────────────────────────────────────────────────────
// TurnStartParams (v2/TurnStartParams.ts)
// Required: threadId, input (both non-optional)
// ──────────────────────────────────────────────────────────────

func TestWireFormat_TurnStartParams_RequiresThreadIdAndInput(t *testing.T) {
	p := TurnStartParams{
		ThreadID: "thr-abc",
		Input:    []UserInput{{Type: "text", Text: "hello"}},
	}
	// TS: { threadId: string, input: Array<UserInput>, ... rest optional }
	requiredKeys(t, p, []string{"threadId", "input"})
}

func TestWireFormat_TurnStartParams_FieldNames(t *testing.T) {
	p := TurnStartParams{
		ThreadID:       "thr-abc",
		Input:          []UserInput{{Type: "text", Text: "run tests"}},
		CWD:            "/tmp/proj",
		ApprovalPolicy: AskForApprovalNever,
		Model:          "codex-mini-latest",
	}
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, key := range []string{"threadId", "input", "cwd", "approvalPolicy", "model"} {
		if _, ok := got[key]; !ok {
			t.Errorf("TurnStartParams: missing key %q in wire output\npayload: %s", key, raw)
		}
	}
}

// TestWireFormat_TurnStartParams_InputVariant_Text verifies the text UserInput
// discriminated-union variant serializes with the correct discriminator field.
func TestWireFormat_TurnStartParams_InputVariant_Text(t *testing.T) {
	input := UserInput{Type: "text", Text: "hello codex"}
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("marshal UserInput: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("json.Unmarshal round-trip: %v", err)
	}
	// TS: { "type": "text", text: string, text_elements: Array<TextElement> }
	// "type" is the discriminator; "text" must be present.
	for _, key := range []string{"type", "text"} {
		if _, ok := got[key]; !ok {
			t.Errorf("UserInput(text): missing key %q\npayload: %s", key, raw)
		}
	}
	if got["type"] != "text" {
		t.Errorf("UserInput: type discriminator: got %v, want text", got["type"])
	}
}

// ──────────────────────────────────────────────────────────────
// TurnInterruptParams (v2/TurnInterruptParams.ts)
// Required: threadId, turnId (both non-optional)
// ──────────────────────────────────────────────────────────────

func TestWireFormat_TurnInterruptParams_RequiresBothIds(t *testing.T) {
	p := TurnInterruptParams{
		ThreadID: "thr-abc",
		TurnID:   "turn-123",
	}
	// TS: { threadId: string, turnId: string }
	requiredKeys(t, p, []string{"threadId", "turnId"})
}

func TestWireFormat_TurnInterruptParams_ZeroValueStillHasKeys(t *testing.T) {
	// Even zero-value strings must appear in the output so codex can parse them.
	p := TurnInterruptParams{}
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("json.Unmarshal round-trip: %v", err)
	}
	for _, key := range []string{"threadId", "turnId"} {
		if _, ok := got[key]; !ok {
			t.Errorf("TurnInterruptParams zero-value: missing key %q\npayload: %s", key, raw)
		}
	}
}

// ──────────────────────────────────────────────────────────────
// ThreadCompactStartParams (v2/ThreadCompactStartParams.ts)
// Required: threadId (non-optional)
// ──────────────────────────────────────────────────────────────

func TestWireFormat_ThreadCompactStartParams_RequiresThreadId(t *testing.T) {
	p := ThreadCompactStartParams{
		ThreadID: "thr-abc",
	}
	// TS: { threadId: string }
	requiredKeys(t, p, []string{"threadId"})
}

func TestWireFormat_ThreadCompactStartParams_ZeroValueHasKey(t *testing.T) {
	// threadId must appear even when empty so codex can parse the request.
	p := ThreadCompactStartParams{}
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("json.Unmarshal round-trip: %v", err)
	}
	if _, ok := got["threadId"]; !ok {
		t.Errorf("ThreadCompactStartParams zero-value: missing key threadId\npayload: %s", raw)
	}
}

// ──────────────────────────────────────────────────────────────
// ReviewStartParams — NOT YET WIRED in Go
// The TS schema defines: { threadId: string, target: ReviewTarget, delivery?: ReviewDelivery | null }
// No Go struct exists for ReviewStartParams or ReviewTarget as of v5.10.0.
// When these types land, uncomment and complete the test below.
// ──────────────────────────────────────────────────────────────

// func TestWireFormat_ReviewStartParams_RequiresThreadIdAndTarget(t *testing.T) {
// 	p := ReviewStartParams{
// 		ThreadID: "thr-abc",
// 		Target:   ReviewTarget{...}, // discriminated union
// 	}
// 	requiredKeys(t, p, []string{"threadId", "target"})
// }
//
// func TestWireFormat_ReviewTarget_UncommittedChanges_Discriminator(t *testing.T) {
// 	target := ReviewTarget{Type: "uncommittedChanges"}
// 	raw, _ := json.Marshal(target)
// 	var got map[string]any
// 	json.Unmarshal(raw, &got)
// 	requiredKeys(t, target, []string{"type"})
// 	if got["type"] != "uncommittedChanges" {
// 		t.Errorf("ReviewTarget discriminator: got %v, want uncommittedChanges", got["type"])
// 	}
// }

// ──────────────────────────────────────────────────────────────
// Wire envelope sanity checks
// ──────────────────────────────────────────────────────────────

func TestWireFormat_JSONRPCRequest_RequiredEnvelopeFields(t *testing.T) {
	req := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
	}
	requiredKeys(t, req, []string{"jsonrpc", "id", "method"})
}

func TestWireFormat_ClientNotification_RequiredFields(t *testing.T) {
	n := ClientNotification{
		JSONRPC: "2.0",
		Method:  "initialized",
	}
	requiredKeys(t, n, []string{"jsonrpc", "method"})
}
