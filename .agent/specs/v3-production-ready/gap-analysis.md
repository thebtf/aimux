# Gap Analysis: aimux v3 — Scaffold vs Production

**Date:** 2026-04-05 (updated 2026-04-05)
**Source:** PRC report + manual code review
**Status:** ALL 13 GAPS RESOLVED (commits 1ae2e4b, 5c158ae, b294018)

## Critical Stubs (code paths that return fake data)

### 1. exec(role="coding") — Pair coding NOT called
**File:** `pkg/server/server.go:434-461`
**What happens:** PairCoding params are prepared (`pairParams`), then discarded (`_ = pairParams`). Execution falls through to direct single-CLI spawn. Constitution P2 violated — solo coding not actually prohibited.
**Impact:** CRITICAL — core v3 value proposition broken.
**Fix:** Replace `_ = pairParams` with `s.orchestrator.Execute(ctx, "pair_coding", pairParams)` and return result.

### 2. agents(action="run") — Returns status string, doesn't execute
**File:** `pkg/server/server.go:706-725`
**What happens:** Agent content loaded, fullPrompt built, but returns JSON with `"status": "delegating to exec"` without actually calling exec.
**Impact:** HIGH — agents tool is non-functional for run action.
**Fix:** Call `s.handleExec()` internally with the built prompt, or create a session+job and run via executor.

### 3. deepresearch (old placeholder) — Still exists alongside real client
**File:** `pkg/tools/deepresearch/deepresearch.go:34-36`
**What happens:** Old `Execute()` function returns "API integration pending". The new `client.go` has real GenAI integration but the old placeholder still exists.
**Impact:** MEDIUM — confusing dead code. Server handler uses new client.go correctly.
**Fix:** Remove old `deepresearch.go` placeholder or convert to facade over client.go.

### 4. PTY Start() / ConPTY Start() — Persistent sessions
**Files:** `pkg/executor/pty/pty.go:146`, `pkg/executor/conpty/conpty.go:143`
**What happens:** `Start()` returns error "not yet implemented".
**Impact:** LOW — LiveStateful sessions via PTY not supported. Pipe executor handles persistent sessions. OnceStateful (spawn-exit-resume) works fine.
**Fix:** Implement Start() → return pipeSession wrapping PTY file descriptor. Or document as Pipe-only for persistent sessions.

## Missing Integration (wired but untested with real CLIs)

### 5. All orchestrator strategies — Tested with mocks only
**Files:** `pkg/orchestrator/*.go`
**What happens:** PairCoding, Dialog, Consensus, Debate, Audit — all use `executor.Run()` which is correctly wired to pipe executor. But tests use mock executor that returns canned responses.
**Impact:** MEDIUM — logic is correct for mock data, but real CLI output parsing (JSONL/JSON/text) untested in strategy context.
**Fix:** Write integration tests that spawn `echo` or a test helper binary via real pipe executor, verify strategy handles real subprocess output.

### 6. Bootstrap prompt injection — Not wired
**What happens:** v2 has `injectBootstrap()` that prepends role-specific TOML prompts. v3 has prompt template engine but it's not called from exec handler.
**Impact:** MEDIUM — CLIs receive raw prompts without system prompt context.
**Fix:** Load prompt template in exec handler based on role, prepend to prompt before spawning CLI.

### 7. Stdin piping for long prompts — Not wired
**What happens:** v2 pipes prompts >6000 chars via stdin (Windows 8191 char limit). v3 has `ShouldUseStdin()` and `BuildStdinArgs()` in driver/template.go but they're never called from server.go.
**Impact:** MEDIUM — long prompts will fail on Windows with "argument too long".
**Fix:** Check `ShouldUseStdin()` before building SpawnArgs in exec handler.

### 8. Completion pattern / inactivity timeout — Not wired
**What happens:** v2 detects `turn.completed` in JSONL output and kills process. v3 has `CompletionPattern` in SpawnArgs but pipe executor doesn't check it.
**Impact:** LOW-MEDIUM — processes may hang after completing output.
**Fix:** Add pattern matching in pipe executor read loop.

## Coverage Gaps (packages with <20% test coverage)

### 9. pkg/server (7.6%) — Tool handler logic untested
**What it does:** 11 MCP tool handlers, ~1000 LOC of param parsing + strategy calling + result formatting.
**Fix:** Mock executor + test each handler with valid/invalid params.

### 10. pkg/executor/conpty (9.1%) — Only platform detection tested
**Fix:** Test Run() with real `echo` command (Windows-specific test).

### 11. pkg/executor/pty (5.7%) — Only platform detection tested  
**Fix:** Test Run() with real `echo` command (Unix-specific test, skip on Windows).

### 12. pkg/tools/deepresearch (7.8%) — Client needs API key
**Fix:** Mock HTTP transport test or test with GOOGLE_API_KEY if available.

## Dead Code

### 13. Old deepresearch.go placeholder
**File:** `pkg/tools/deepresearch/deepresearch.go`
**What:** `DeepResearch` struct with `Execute()` returning "pending". Superseded by `client.go`.
**Fix:** Remove or refactor.

## Priority Order for Implementation

| Priority | Gap | Effort | Impact |
|----------|-----|--------|--------|
| P0 | #1 Pair coding stub | 30min | CRITICAL — core value |
| P0 | #2 Agents run stub | 20min | HIGH — tool broken |
| P1 | #6 Bootstrap prompts | 1h | MEDIUM — CLI context |
| P1 | #7 Stdin piping | 30min | MEDIUM — long prompts |
| P1 | #9 Server tests | 2h | MEDIUM — coverage |
| P2 | #5 Strategy integration tests | 2h | MEDIUM — confidence |
| P2 | #8 Completion pattern | 1h | LOW-MEDIUM |
| P2 | #3 Dead code cleanup | 15min | LOW |
| P3 | #4 PTY Start() | 2h | LOW |
| P3 | #10-12 Coverage | 3h | LOW |
