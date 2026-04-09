# Implementation Plan: mcp-aimux v3 — Full Go Rewrite

**Spec:** .agent/specs/go-rewrite-v3/spec.md
**Constitution:** .agent/specs/constitution.md
**ADR:** .agent/arch/decisions/ADR-014-go-rewrite.md
**Created:** 2026-04-05
**Status:** Draft

## Tech Stack

| Component | Choice | Rationale |
|-----------|--------|-----------|
| Language | Go 1.25+ | Native ConPTY, goroutines, single binary, race detector |
| MCP SDK | `github.com/mark3labs/mcp-go` v0.47+ | 8.5K stars, full protocol, released yesterday |
| Config | `gopkg.in/yaml.v3` | YAML per Decision 13. Mature, correct formatting |
| SQLite | `modernc.org/sqlite` | Pure Go, no CGO. WAL mode for concurrent reads |
| ConPTY | `golang.org/x/sys/windows` | Native CreatePseudoConsole. Linux: `os/exec` + PTY |
| PTY (Linux) | `github.com/creack/pty` | Standard Go PTY library |
| UUID | `github.com/google/uuid` | UUIDv7 for session/job IDs |
| ANSI strip | Custom (~50 LOC) | From ccg-workflow sanitizeOutput pattern |
| Testing | `testing` stdlib + `github.com/stretchr/testify` | Table-driven + assertions |

## Architecture

```
cmd/
  aimux/main.go           # Entry point, signal handling, CLI flags

pkg/
  config/                 # YAML parsing, CLI profile discovery
  types/                  # Shared types, interfaces, errors
  routing/                # Role→CLI resolution, env overrides
  executor/               # Executor interface + 3 implementations
    conpty/               # Windows ConPTY executor
    pty/                  # Linux/Mac PTY executor  
    pipe/                 # Fallback pipe+JSON executor
    pipeline/             # ANSI stripper, event filter, content router
  session/                # In-memory state + WAL + SQLite snapshots
  server/                 # MCP server, tool registry, resources, prompts
  tools/                  # 10 tool implementations (one pkg each)
    exec/
    status/
    sessions/
    audit/
    consensus/
    debate/
    dialog/
    think/
    investigate/
    agents/
  orchestrator/           # Unified orchestrator (Strategy pattern)
  driver/                 # CLI profile loader from cli.d/
  parser/                 # JSONL, JSON, stream-json, text parsers
  prompt/                 # Template engine (prompts.d/ + includes)
  logger/                 # Async file logger (Decision 16)

config/
  default.yaml            # Server config
  cli.d/                  # Per-CLI plugin dirs
    codex/profile.yaml
    gemini/profile.yaml
    claude/profile.yaml
    qwen/profile.yaml
    aider/profile.yaml
    droid/profile.yaml
    opencode/profile.yaml
  prompts.d/              # Composable prompt templates
    styles/               # Output style templates
  agents/                 # Built-in Loom Agent definitions
```

## Data Model

### Session
| Field | Type | Notes |
|-------|------|-------|
| ID | UUIDv7 | Primary key |
| CLI | string | Which CLI |
| Mode | SessionMode | Live/OnceStateful/OnceStateless |
| CLISessionID | string | CLI's own session ID for resume |
| PID | int | 0 for completed |
| Status | enum | created/running/completing/completed/failed |
| Turns | int | Turn counter |
| CWD | string | Working directory |
| CreatedAt | time.Time | |
| LastActiveAt | time.Time | |

### Job
| Field | Type | Notes |
|-------|------|-------|
| ID | UUIDv7 | Primary key |
| SessionID | UUIDv7 | FK to Session |
| CLI | string | |
| Status | enum | created/running/completing/completed/failed |
| Progress | string | Last progress line |
| Content | string | Final output |
| ExitCode | int | |
| Error | *TypedError | Structured error (nullable) |
| PollCount | int | Anti-polling counter |
| Pheromones | map[string]string | discovery/warning/repellent markers |
| Pipeline | *PipelineStats | Audit-specific timing |

### LiveSession (in-memory only)
| Field | Type | Notes |
|-------|------|-------|
| Session | *Session | Embedded |
| Process | *os.Process | Alive process handle |
| Stdin | io.Writer | Send prompts |
| Events | chan Event | Output stream |
| Ctx | context.Context | Cancellation |
| Mu | sync.Mutex | Serialize Send() |

## API Contracts

### exec tool
```go
type ExecInput struct {
    CLI             string `json:"cli,omitempty"`
    Role            string `json:"role,omitempty"`
    Prompt          string `json:"prompt"`
    CWD             string `json:"cwd,omitempty"`
    Model           string `json:"model,omitempty"`
    ReasoningEffort string `json:"reasoning_effort,omitempty"`
    Async           *bool  `json:"async,omitempty"`
    SessionID       string `json:"session_id,omitempty"`
    Agent           string `json:"agent,omitempty"`
    Complex         bool   `json:"complex,omitempty"`     // NEW: pair review returns to caller
    OutputStyle     string `json:"output_style,omitempty"` // NEW: prompt style injection
}

type ExecResult struct {
    JobID        string       `json:"job_id,omitempty"`
    SessionID    string       `json:"session_id"`
    Status       string       `json:"status"`
    Content      string       `json:"content,omitempty"`
    // Pair review fields (fire-and-forget mode)
    FilesChanged []string     `json:"files_changed,omitempty"`
    HunksApplied int          `json:"hunks_applied,omitempty"`
    ReviewReport *ReviewReport `json:"review_report,omitempty"`
    // Pair review fields (complex mode)
    DriverDiff    string       `json:"driver_diff,omitempty"`
    ReviewVerdict *ReviewVerdict `json:"review_verdict,omitempty"`
}
```

### Executor interface
```go
type Executor interface {
    Run(ctx context.Context, args SpawnArgs) (*Result, error)
    Start(ctx context.Context, args SpawnArgs) (Session, error)
    SupportsStreaming() bool
}

type Session interface {
    ID() string
    Send(prompt string) (*Result, error)
    Stream(prompt string) <-chan Event
    Close() error
}
```

### Orchestrator interface
```go
type Strategy interface {
    Name() string
    Execute(ctx context.Context, params OrchestratorParams) (*OrchestratorResult, error)
}

// Implementations: PairCoding, SequentialDialog, ParallelConsensus,
// StructuredDebate, AuditPipeline
```

## Phases

### Phase 0: Foundation (3 days)
- `cmd/aimux/main.go` — entry point, signal handling
- `pkg/types/` — all shared types, interfaces, typed errors
- `pkg/config/` — YAML parsing, cli.d/ discovery, server config
- `pkg/routing/` — role→CLI resolution, env overrides
- `pkg/logger/` — async file logger
- **Deliverable:** binary that starts, parses config, discovers CLIs, logs

### Phase 1: Executor Engine (5 days)
- `pkg/executor/` — Executor interface
- `pkg/executor/conpty/` — Windows ConPTY implementation
- `pkg/executor/pty/` — Linux/Mac PTY implementation
- `pkg/executor/pipe/` — Fallback pipe+JSON implementation
- `pkg/executor/pipeline/` — ANSI stripper, event filter, content router
- `pkg/parser/` — JSONL, JSON, text parsers
- `pkg/driver/` — CLI profile loader, template engine, feature flags
- **Deliverable:** binary that spawns any CLI, parses output, returns structured result

### Phase 2: Session & MCP Server (4 days)
- `pkg/session/` — in-memory state, WAL journal, SQLite snapshots, GC
- `pkg/server/` — MCP server via mcp-go, tool registry, resources, prompts
- `pkg/tools/exec/` — exec tool (OnceStateful mode)
- `pkg/tools/status/` — status tool (read-only)
- `pkg/tools/sessions/` — sessions tool
- **Deliverable:** functional MCP server with exec + status + sessions. Can replace v2 for basic exec.

### Phase 3: Pair Coding Pipeline (5 days)
- `pkg/orchestrator/` — unified orchestrator, Strategy interface
- `pkg/orchestrator/pair.go` — PairCoding strategy (diff-only driver + sonnet reviewer)
- `pkg/tools/exec/` — pair integration (fire-and-forget + complex modes)
- `pkg/prompt/` — template engine, prompts.d/ loader, includes, output styles
- LiveStateful session support in executor
- **Deliverable:** `exec(role="coding")` = mandatory pair with diff review. Core v3 value proposition.

### Phase 4: Multi-Model Orchestration (4 days)
- `pkg/orchestrator/dialog.go` — SequentialDialog strategy
- `pkg/orchestrator/consensus.go` — ParallelConsensus strategy
- `pkg/orchestrator/debate.go` — StructuredDebate strategy
- `pkg/tools/dialog/` — dialog tool
- `pkg/tools/consensus/` — consensus tool
- `pkg/tools/debate/` — debate tool
- **Deliverable:** all multi-model tools functional

### Phase 5: Analysis Tools (4 days)
- `pkg/tools/think/` — 16 thinking patterns + session state
- `pkg/tools/investigate/` — 6 domains, convergence tracking, reports
- `pkg/tools/audit/` — audit pipeline v2 (scan→validate→investigate)
- `pkg/tools/agents/` — Loom Agent v2 discovery + workflow execution
- **Deliverable:** all analysis/audit tools functional

### Phase 6: Remaining + Deep Research (3 days)
- `pkg/tools/deepresearch/` — Google GenAI integration
- `pkg/agents/` — Loom Agent registry, 9-source discovery
- Pheromone metadata on jobs
- Domain trust hierarchies in review
- File assignment matrix for parallel agents
- **Deliverable:** feature-complete binary

### Phase 7: Verification + Release (4 days)
- Generate feature-parity.toml from v2 codebase
- Side-by-side verification (v2 TS vs v3 Go)
- Stress test: 10,000 tool calls, EPIPE injection, concurrent jobs
- Race detector clean
- Cross-compile (Windows/Linux/macOS)
- mcp-mux compatibility test
- **Deliverable:** release candidate

## Library Decisions

| Component | Library | Version | Rationale |
|-----------|---------|---------|-----------|
| MCP SDK | `mark3labs/mcp-go` | v0.47+ | 8.5K stars, full protocol, active development |
| YAML | `gopkg.in/yaml.v3` | v3 | Mature, correct output formatting |
| SQLite | `modernc.org/sqlite` | latest | Pure Go, no CGO, WAL mode |
| ConPTY | `golang.org/x/sys/windows` | latest | Official Go team, native API |
| PTY | `creack/pty` | v1.1+ | Standard Go PTY, well maintained |
| UUID | `google/uuid` | v1.6+ | UUIDv7 support |
| Testing | `stretchr/testify` | v1.9+ | Assertions, mocks, suites |
| ANSI strip | Custom | — | ~50 LOC, from ccg-workflow pattern |
| Process tree kill | Custom | — | Platform-specific, from v2 + ccg-workflow patterns |
| Logging | Custom | — | Async file logger from ccg-wrapper pattern (Decision 16) |

## Unknowns and Risks

| Unknown | Impact | Resolution Strategy |
|---------|--------|-------------------|
| ConPTY + codex interaction | HIGH | Phase 1 PoC: spawn codex via ConPTY, verify text output is unbuffered |
| mcp-go task support | MED | Check v0.47 API before Phase 2. If missing, implement custom |
| Spark model detection latency | LOW | Probe once at startup, cache result |
| go test -race false positives | LOW | Triage in Phase 7, suppress known stdlib races |

## Constitution Compliance

| Principle | How Addressed |
|-----------|---------------|
| 1. No CLI Writes Files | Executor runs DiffOnly/ReadOnly. Pair apply in orchestrator. |
| 2. Always Pair | Phase 3: PairCoding strategy mandatory for exec(coding) |
| 3. Correct Over Simple | 18 ADR decisions, not shortcuts |
| 4. ConPTY-First | Phase 1: executor with runtime detection |
| 5. Context Everywhere | All goroutines receive context.Context |
| 6. Push Not Poll | Phase 2: channel-based progress in session manager |
| 7. Typed Errors | Phase 0: types/ package defines all error types |
| 8. Single Source Config | Phase 0: YAML config, no hardcoded values |
| 9. CLI Plugin Dirs | Phase 0: cli.d/ discovery in config/ |
| 10. Verify Before Ship | Phase 7: feature-parity.toml verification |
| 11. Immutable Default | All public types use value semantics, no pointer sharing |
| 12. Evidence-Based | ADR-014 cites benchmarks, bug counts, production data |
| 13. Domain Trust | Phase 6: trust_domain in role config |
| 14. Composable Prompts | Phase 3: prompts.d/ engine |
