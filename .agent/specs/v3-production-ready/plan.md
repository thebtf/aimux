# Implementation Plan: aimux v3 Production Readiness

**Spec:** .agent/specs/v3-production-ready/spec.md
**Constitution:** .agent/specs/constitution.md
**Baseline:** .agent/specs/go-rewrite-v3/ (scaffold complete, 113 tests)
**Created:** 2026-04-05
**Status:** Draft

## Tech Stack

| Component | Choice | Rationale |
|-----------|--------|-----------|
| PTY (Unix) | `github.com/creack/pty` v1.1+ | Standard Go PTY, High reputation, 25 snippets on Context7. API: `pty.Start(cmd)` → `*os.File` |
| ConPTY (Windows) | `golang.org/x/sys/windows` | Official Go team, CreatePseudoConsole via syscall |
| GenAI SDK | `google.golang.org/genai` | Official Google Gen AI Go SDK, 889 snippets, v1.23+. Supports Gemini API + Interactions |
| Existing | All from go-rewrite-v3 plan | mcp-go v0.47, modernc.org/sqlite, gopkg.in/yaml.v3, google/uuid |

## Architecture

No new packages — all work is wiring existing scaffold packages together:

```
pkg/executor/pty/pty.go        — Replace stub with creack/pty.Start + read loop
pkg/executor/conpty/conpty.go  — Replace stub with CreatePseudoConsole syscalls
pkg/server/server.go           — Wire tool handlers → orchestrator strategies
pkg/tools/deepresearch/        — Add Google GenAI API client
```

Key wiring pattern for each tool handler:
```
MCP request → parse params → create Orchestrator → execute Strategy → return result
```

The Server struct already holds executor, sessions, jobs, breakers. Add orchestrator field.

## API Contracts

### Server.orchestrator field (NEW)
```go
type Server struct {
    // ... existing fields ...
    orchestrator *orchestrator.Orchestrator  // NEW: strategy executor
    agentReg     *agents.Registry            // NEW: agent discovery
}
```

### Wired tool handler pattern
```go
func (s *Server) handleConsensus(ctx, req) (*CallToolResult, error) {
    topic := req.RequireString("topic")
    params := types.StrategyParams{Prompt: topic, CLIs: enabledCLIs, ...}
    result, err := s.orchestrator.Execute(ctx, "consensus", params)
    if err != nil { return TypedError response }
    return mcp.NewToolResultText(json.Marshal(result))
}
```

## Phases

### Phase 1: Wire Orchestrator into Server (FR-3, FR-4, FR-6)
- Add `orchestrator` and `agentReg` fields to Server struct
- Initialize all 5 strategies in New() with the real executor
- Replace 6 placeholder tool handlers with real strategy calls
- Wire agents tool to AgentRegistry.Discover/List/Find/Run
- Wire exec(coding) → PairCoding strategy end-to-end
- **Deliverable:** All 10 tools return real results, zero placeholders

### Phase 2: PTY Executor (FR-2)
- Implement `pkg/executor/pty/pty.go` Run() with creack/pty
- `pty.Start(cmd)` → read loop with ANSI stripping → Result
- Context cancellation → process kill → partial output
- Timeout via timer + kill
- Integration test: spawn `echo` via PTY
- **Deliverable:** PTY executor functional on Linux/Mac

### Phase 3: ConPTY Executor (FR-1)
- Implement `pkg/executor/conpty/conpty.go` Run() with x/sys/windows
- CreatePseudoConsole → CreateProcess with pseudo console → ReadFile on output pipe
- Context/timeout handling identical to PTY
- Build-tag guarded: `//go:build windows`
- **Deliverable:** ConPTY executor functional on Windows

### Phase 4: DeepResearch API (FR-5)
- Add `google.golang.org/genai` dependency
- Implement API client in `pkg/tools/deepresearch/`
- File upload via Files API, progress tracking, caching
- Wire into MCP tool handler
- **Deliverable:** deepresearch tool calls real API

### Phase 5: GitHub + CI (FR-7, NFR-2)
- Create GitHub repo `thebtf/aimux`
- Push all commits
- Verify CI passes (go test -race on Linux, vet, lint)
- **Deliverable:** CI green, race detector clean

### Phase 6: Verification
- Grep for "not yet implemented|wiring pending|pending" → must be 0
- Smoke test: all 10 tools via MCP protocol
- mcp-mux compatibility test
- **Deliverable:** Production ready

## Library Decisions

| Component | Library | Version | Rationale |
|-----------|---------|---------|-----------|
| PTY | `creack/pty` | v1.1+ | Standard Go PTY, well maintained, simple API |
| ConPTY | `golang.org/x/sys/windows` | latest | Official, CreatePseudoConsole API |
| GenAI | `google.golang.org/genai` | v1.23+ | Official Google SDK, full Gemini API |
| ANSI strip | Custom (existing) | — | Already in pkg/executor/pipeline/ansi.go |

## Unknowns and Risks

| Unknown | Impact | Resolution Strategy |
|---------|--------|-------------------|
| ConPTY + codex buffering | HIGH | Phase 3 PoC: spawn codex via ConPTY, check if text output is truly unbuffered |
| google.golang.org/genai Interactions API | MED | Check if Deep Research is exposed in SDK or needs raw HTTP |
| creack/pty on macOS ARM64 | LOW | CI matrix includes macos-latest, will catch issues |

## Constitution Compliance

| Principle | How Addressed |
|-----------|---------------|
| P2 Always Pair | Phase 1: exec(coding) → PairCoding strategy end-to-end |
| P4 ConPTY-First | Phase 3: ConPTY on Windows, PTY on Unix, Pipe fallback |
| P5 Context Everywhere | All executors use context.Context for cancel/timeout |
| P6 Push Not Poll | Strategies use channel-based progress (existing) |
| P7 Typed Errors | NFR-5: TypedError propagation through wired handlers |
| P8 Single Config | All tool behavior via YAML config (existing) |
| P9 CLI Plugin Dirs | cli.d/ profiles (existing) |
| P10 Verify Before Ship | Phase 6: grep for placeholders, smoke test |
