// Package codex implements the Codex app-server executor for aimux.
//
// It exposes a pool of long-lived `codex app-server` subprocesses managed by
// the aimux daemon. Each execution is a Loom task, giving it SQLite-backed
// persistence, crash recovery, and the uniform status/cancel interface.
//
// Wire protocol: JSON-RPC 2.0 over stdio JSONL framing (one JSON object per
// line). All struct field names are verified against live codex 0.128.0
// responses — see architecture.md §10 evidence table.
package codex

import "encoding/json"

// --- JSON-RPC 2.0 envelope types ---

// JSONRPCRequest is the outbound request envelope.
type JSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// JSONRPCResponse is the inbound response envelope.
// Exactly one of Result or Error is set.
type JSONRPCResponse struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      int64            `json:"id"`
	Result  json.RawMessage  `json:"result,omitempty"`
	Error   *JSONRPCError    `json:"error,omitempty"`
}

// JSONRPCError represents a JSON-RPC error object.
type JSONRPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *JSONRPCError) Error() string {
	return e.Message
}

// JSONRPCNotification is an inbound server notification (no id field).
type JSONRPCNotification struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// inboundMessage is used to detect whether an inbound line is a response,
// notification, or server request.
type inboundMessage struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *int64           `json:"id,omitempty"`
	Method  string           `json:"method,omitempty"`
	Result  json.RawMessage  `json:"result,omitempty"`
	Error   *JSONRPCError    `json:"error,omitempty"`
	Params  json.RawMessage  `json:"params,omitempty"`
}

// isResponse returns true when the message is a response to a call we made.
func (m *inboundMessage) isResponse() bool {
	return m.ID != nil && m.Method == "" && (m.Result != nil || m.Error != nil)
}

// isNotification returns true when the message is a server-push notification.
func (m *inboundMessage) isNotification() bool {
	return m.ID == nil && m.Method != ""
}

// isServerRequest returns true when the message is a server-initiated request.
// We auto-reject these with -32601 MethodNotFound.
func (m *inboundMessage) isServerRequest() bool {
	return m.ID != nil && m.Method != ""
}

// --- Protocol types (v2 subset) ---
// Field names are verified against live codex 0.128.0 wire output.
// See architecture.md §10 evidence table.

// SandboxMode is the 3-value string enum from v2/SandboxMode.ts (VERIFIED).
type SandboxMode string

const (
	SandboxModeReadOnly        SandboxMode = "read-only"
	SandboxModeWorkspaceWrite  SandboxMode = "workspace-write"
	SandboxModeDangerFullAccess SandboxMode = "danger-full-access"
)

// AskForApproval mirrors the approval policy (subset of v2/AskForApproval.ts).
type AskForApproval string

const (
	AskForApprovalNever     AskForApproval = "never"
	AskForApprovalOnRequest AskForApproval = "on-request"
	AskForApprovalUntrusted AskForApproval = "untrusted"
	AskForApprovalOnFailure AskForApproval = "on-failure"
)

// InitializeCapabilities controls capability negotiation on connect.
// experimentalApi is always sent (matching plugin behaviour: always false).
// optOutNotificationMethods suppresses high-volume delta variants (ADR-011).
type InitializeCapabilities struct {
	ExperimentalApi           bool     `json:"experimentalApi"`
	OptOutNotificationMethods []string `json:"optOutNotificationMethods,omitempty"`
}

// ClientInfo identifies the aimux client to the codex app-server.
// Required by initialize RPC per v2/ClientInfo.ts — must not be omitted.
type ClientInfo struct {
	Name    string `json:"name"`
	Title   string `json:"title,omitempty"`
	Version string `json:"version"`
}

// InitializeParams is sent as the first request to codex app-server.
// ClientInfo has no omitempty — codex rejects a missing or null clientInfo.
type InitializeParams struct {
	ClientInfo   ClientInfo             `json:"clientInfo"`
	Capabilities InitializeCapabilities `json:"capabilities,omitempty"`
}

// InitializeResult is the response to initialize.
type InitializeResult struct {
	SessionID string `json:"sessionId,omitempty"`
}

// ClientNotification is a client-to-server notification (no response expected).
// Used to send `initialized` after initialize response.
type ClientNotification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
}

// Thread mirrors v2/Thread.ts (minimal subset for v1).
// VERIFIED: thread/start response is result.thread.id (not result.threadId).
type Thread struct {
	ID       string `json:"id"`
	CWD      string `json:"cwd,omitempty"`
	Ephemeral bool   `json:"ephemeral,omitempty"`
	Preview  string `json:"preview,omitempty"`
	Path     string `json:"path,omitempty"`
}

// Turn mirrors v2/Turn.ts (minimal subset for v1).
// VERIFIED: turn/start response is result.turn.id (not result.turnId).
type Turn struct {
	ID          string    `json:"id"`
	Status      TurnStatus `json:"status,omitempty"`
	StartedAt   *int64    `json:"startedAt,omitempty"`
	CompletedAt *int64    `json:"completedAt,omitempty"`
	Error       *TurnError `json:"error,omitempty"`
}

// TurnStatus mirrors v2/TurnStatus.ts values.
type TurnStatus string

const (
	TurnStatusRunning   TurnStatus = "running"
	TurnStatusCompleted TurnStatus = "completed"
	TurnStatusFailed    TurnStatus = "failed"
	TurnStatusCancelled TurnStatus = "cancelled"
)

// TurnError holds error details when a turn fails.
type TurnError struct {
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

// UserInput represents a single user message input item.
// v1 only uses the text variant.
type UserInput struct {
	Type string `json:"type"` // "text"
	Text string `json:"text"`
}

// ThreadStartParams mirrors v2/ThreadStartParams.ts (subset for v1).
type ThreadStartParams struct {
	Model          string         `json:"model,omitempty"`
	CWD            string         `json:"cwd,omitempty"`
	ApprovalPolicy AskForApproval `json:"approvalPolicy,omitempty"`
	Sandbox        SandboxMode    `json:"sandbox,omitempty"`
	Ephemeral      bool           `json:"ephemeral"`
}

// ThreadStartResponse mirrors v2/ThreadStartResponse.ts.
// VERIFIED: result.thread.id is the correct field path.
type ThreadStartResponse struct {
	Thread Thread `json:"thread"`
}

// TurnStartParams mirrors v2/TurnStartParams.ts (subset for v1).
type TurnStartParams struct {
	ThreadID       string         `json:"threadId"`
	Input          []UserInput    `json:"input"`
	CWD            string         `json:"cwd,omitempty"`
	ApprovalPolicy AskForApproval `json:"approvalPolicy,omitempty"`
	Model          string         `json:"model,omitempty"`
	OutputSchema   interface{}    `json:"outputSchema,omitempty"`
}

// TurnStartResponse mirrors v2/TurnStartResponse.ts.
// VERIFIED: result.turn.id is the correct field path.
type TurnStartResponse struct {
	Turn Turn `json:"turn"`
}

// ThreadResumeParams mirrors v2/ThreadResumeParams.ts (subset for v1).
type ThreadResumeParams struct {
	ThreadID       string         `json:"threadId"`
	CWD            string         `json:"cwd,omitempty"`
	ApprovalPolicy AskForApproval `json:"approvalPolicy,omitempty"`
	Sandbox        SandboxMode    `json:"sandbox,omitempty"`
	ExcludeTurns   bool           `json:"excludeTurns,omitempty"`
}

// ThreadResumeResponse mirrors v2/ThreadResumeResponse.ts.
type ThreadResumeResponse struct {
	Thread Thread `json:"thread"`
}

// TurnInterruptParams mirrors v2/TurnInterruptParams.ts (VERIFIED exact fields).
type TurnInterruptParams struct {
	ThreadID string `json:"threadId"`
	TurnID   string `json:"turnId"`
}

// TurnInterruptResponse is the (empty) response to turn/interrupt.
type TurnInterruptResponse struct{}

// ThreadItem is a discriminated union. v1 only processes agentMessage items.
// Other types are parsed as raw JSON and stored as Unknown.
// VERIFIED: item.text carries agent message text in item/completed notifications.
type ThreadItem struct {
	Type string `json:"type"`

	// agentMessage fields (VERIFIED: item.text)
	ID   string `json:"id,omitempty"`
	Text string `json:"text,omitempty"`
}

// ItemCompletedNotification mirrors v2/ItemCompletedNotification.ts.
// VERIFIED: item.text carries agent text when item.type=="agentMessage".
type ItemCompletedNotification struct {
	Item     ThreadItem `json:"item"`
	ThreadID string     `json:"threadId"`
	TurnID   string     `json:"turnId"`
}

// TurnCompletedNotification mirrors v2/TurnCompletedNotification.ts.
type TurnCompletedNotification struct {
	ThreadID string `json:"threadId"`
	Turn     Turn   `json:"turn"`
}

// --- Loom metadata types ---

// CodexTaskMeta holds Codex-specific state stored in Loom task.Metadata.
// Serialized as JSON in the tasks.metadata column.
type CodexTaskMeta struct {
	// ThreadID is the codex thread.id (VERIFIED: result.thread.id).
	ThreadID string `json:"thread_id"`

	// RootThreadID is set on resume; equals ThreadID for fresh tasks.
	RootThreadID string `json:"root_thread_id,omitempty"`

	// TurnID is the codex turn.id (VERIFIED: result.turn.id).
	TurnID string `json:"turn_id,omitempty"`

	// JobClass is "review" | "task" | "write-task" | "danger".
	JobClass string `json:"job_class"`

	// ResumeFallback is set to true when thread/resume failed and a fresh thread was used.
	ResumeFallback bool `json:"resume_fallback,omitempty"`

	// OutputSchema is an optional JSON Schema object passed to TurnStartParams.OutputSchema.
	// Used by review jobs to constrain Codex output to the expected findings/decision shape,
	// reducing the chance of parseGateDecision receiving malformed JSON.
	OutputSchema any `json:"output_schema,omitempty"`

	// LastInputTokens is the cumulative input token count written by Worker on each turn
	// completion. Readable via codex_status with include_content=true (FR-12).
	LastInputTokens int64 `json:"last_input_tokens,omitempty"`

	// CompactionCount tracks how many times Worker triggered compaction for this task.
	// Readable via codex_status with include_content=true (FR-12).
	CompactionCount int `json:"compaction_count,omitempty"`
}

// --- Thread list types (for Resumer fallback path) ---

// ThreadSummary is a minimal mirror of a thread row returned by thread/list.
// VERIFIED (probe-2026-05-07 OQ-1): response is result.data[].
type ThreadSummary struct {
	ID        string `json:"id"`
	CWD       string `json:"cwd,omitempty"`
	Preview   string `json:"preview,omitempty"`
	Ephemeral bool   `json:"ephemeral,omitempty"`
	CreatedAt int64  `json:"createdAt,omitempty"`
	UpdatedAt int64  `json:"updatedAt,omitempty"`
}

// ThreadListParams mirrors the thread/list request parameters.
// VERIFIED (probe-2026-05-07 OQ-1): UseStateDbOnly=true cuts 19s→72ms (270x).
// Always pass UseStateDbOnly: true. Never omit (default=false triggers full JSONL scan).
type ThreadListParams struct {
	SearchTerm     string   `json:"searchTerm,omitempty"`
	SourceKinds    []string `json:"sourceKinds,omitempty"`
	Limit          int      `json:"limit,omitempty"`
	UseStateDbOnly bool     `json:"useStateDbOnly"` // ALWAYS true — 270x speedup
}

// ThreadListResponse mirrors the thread/list response.
// VERIFIED (probe-2026-05-07 OQ-1): array is at result.data (NOT result.threads).
type ThreadListResponse struct {
	Data           []ThreadSummary `json:"data"`
	NextCursor     string          `json:"nextCursor,omitempty"`
	BackwardCursor string          `json:"backwardsCursor,omitempty"`
}

// Notification methods we opt out of (ADR-011).
// These are high-volume delta variants that add no information for our use case.
// Agent message text is fully available in item/completed (VERIFIED: Test 1).
var OptOutNotificationMethods = []string{
	"item/agentMessage/delta",
	"item/reasoning/summaryTextDelta",
	"item/reasoning/summaryPartAdded",
	"item/reasoning/textDelta",
}

// Notification methods we care about.
const (
	MethodItemCompleted      = "item/completed"
	MethodTurnCompleted      = "turn/completed"
	MethodInitialized        = "initialized"
	MethodTokenUsageUpdated  = "thread/tokenUsage/updated"
)

// ThreadCompactStartParams mirrors the thread/compact/start request.
// VERIFIED (probe-2026-05-07 OQ-7): params={threadId} only.
type ThreadCompactStartParams struct {
	ThreadID string `json:"threadId"`
}

// TokenUsage holds per-thread cumulative token counts from thread/tokenUsage/updated.
// VERIFIED (probe-2026-05-07 OQ-7): totals are cumulative and do not decrease.
// Compaction reduces future per-turn input cost; existing history totals are unchanged.
type TokenUsage struct {
	InputTokens       int64 `json:"inputTokens"`
	CachedInputTokens int64 `json:"cacheReadTokens"`
	OutputTokens      int64 `json:"outputTokens"`
}

// TokenUsageNotification mirrors the thread/tokenUsage/updated notification params.
type TokenUsageNotification struct {
	ThreadID string     `json:"threadId"`
	Usage    TokenUsage `json:"usage"`
}
