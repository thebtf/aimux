# Security Scan Report — aimux v3

**Date:** 2026-04-05
**Scanner:** security-scan-specialist
**Codebase:** `D:\Dev\aimux` (Go, MCP server, aimux v3)
**Go version:** go1.25.4
**Scope:** Full codebase — all packages

---

## Executive Summary

| Severity | Count |
|----------|-------|
| P1 Critical | 1 |
| P2 High | 5 |
| P3 Medium | 4 |
| P4 Low | 3 |
| **Total** | **13** |

The most significant finding is an **unenforced concurrent job limit** (P1): `MaxConcurrentJobs` is configured but never read at spawn time, allowing unbounded process creation. Five P2 findings cover CWD path traversal, model/effort argument injection, unvalidated agent content injection, stale Go runtime CVEs, and merged-env inheritance stripping. Immediate action is required on the P1 and P2 findings before any production deployment.

---

## P1 Critical

### FIND-001 — MaxConcurrentJobs limit is configured but never enforced

**File:** `pkg/server/server.go` (handleExec, handleAgents, handleAudit), `pkg/config/config.go:19`
**CVSS category:** Unbounded resource consumption / Denial of Service

`default.yaml` sets `max_concurrent_jobs: 10` and `applyDefaults` ensures the value is set (line 196). However, no code in `handleExec`, `handleAgents`, `handleAudit`, or any executor path reads `cfg.Server.MaxConcurrentJobs` before calling `go s.executeJob(...)` or `go s.executePairCoding(...)`. Each async call unconditionally spawns a new goroutine that spawns a child process.

Confirmed with grep:

```
grep -n "MaxConcurrentJobs" pkg/server/server.go  → zero matches
```

An attacker with MCP access can issue an unbounded number of `exec(async=true)` calls, forking an arbitrary number of CLI child processes (codex, gemini, etc.) until memory or OS process limits are exhausted.

**Impact:** Full server DoS; potential OOM kill; file descriptor exhaustion.

**Remediation:** Before calling `go s.executeJob(...)`, check `s.jobs.CountRunning()` against `s.cfg.Server.MaxConcurrentJobs` and return an error if the limit is reached.

---

## P2 High

### FIND-002 — CWD parameter accepted without path validation (path traversal)

**Files:** `pkg/server/server.go:399`, `pkg/server/server.go:824`, `pkg/server/server.go:895`

The `cwd` parameter in `exec`, `agents run`, and `audit` tools is forwarded directly to `cmd.Dir` in all three executors (`pipe.go:41`, `conpty.go:62`, `pty.go`):

```go
cwd := request.GetString("cwd", "")
// ...
cmd.Dir = args.CWD  // no validation
```

There is no call to `filepath.Clean`, `filepath.Abs`, or `os.Stat` to verify the directory exists or is within an expected root. A caller can supply `cwd: "../../etc"` or an absolute path outside the project. While `exec.Command` itself will error if the directory does not exist, a valid path outside the project root causes the CLI tool to execute with that working directory, potentially accessing sensitive files or being influenced by hostile configuration files (e.g., `.codex/config`, `.claude/settings`) planted outside the project.

**Impact:** CLI tools run with arbitrary working directories; potentially reads hostile config from attacker-controlled paths.

**Remediation:** Validate `cwd` with `filepath.Abs` + `os.Stat` before use. Optionally reject paths outside a configured root or the server's working directory.

---

### FIND-003 — User-supplied `model` and `reasoning_effort` injected into CLI args without validation

**File:** `pkg/server/server.go:1156–1167` (`buildArgs`), `pkg/driver/template.go:40–52`

Both `model` and `effort` arrive from the MCP request as free-form strings and are appended directly as CLI flag values:

```go
args = append(args, profile.ModelFlag, model)
// and
val := fmt.Sprintf(profile.Reasoning.FlagValueTemplate, effort)
args = append(args, profile.Reasoning.Flag, val)
```

There is no allowlist validation against known model names or effort levels. Since `exec.Command` passes each element as a separate argument (no shell splitting), classic shell injection (`;`, `&&`) is not possible here. However:

1. A caller can supply a model string containing spaces (e.g., `"gpt-4 --dangerous-flag value"`) — `exec.Command` will pass it as a single argument to the CLI, but many CLI tools accept `--model "value"` and will parse internal flags differently.
2. `fmt.Sprintf(profile.Reasoning.FlagValueTemplate, effort)` uses `%s` substitution from user input. If `FlagValueTemplate` contains additional format verbs, `effort` could cause `fmt.Sprintf` to read stack values.
3. For codex, `FlagValueTemplate = 'model_reasoning_effort="{{.Level}}"'` — this is a Go template string used in `driver/template.go` but `buildArgs` in `server.go` calls `fmt.Sprintf` with the template directly (line 1162), meaning `{{.Level}}` is never substituted; instead `%s` is the operative verb. If `effort` contains `%` characters, this will produce malformed output or a runtime format error.

**Impact:** Malformed CLI invocations; potential flag injection depending on CLI argument parser behavior; format string misuse.

**Remediation:**
- Validate `model` against a configured allowlist or regex `^[a-zA-Z0-9._-]+$`.
- Validate `effort` against `profile.Reasoning.Levels` (the `ValidateReasoningEffort` helper in `driver/template.go` already exists but is never called from `handleExec`).
- Fix the `fmt.Sprintf` / template mismatch in `buildArgs` — either use `strings.ReplaceAll` (matching `template.go`) or `text/template`, not `fmt.Sprintf`.

---

### FIND-004 — Agent content injected into CLI prompt without sanitization

**File:** `pkg/server/server.go:819`

```go
fullPrompt := agent.Content + "\n\n" + prompt
```

`agent.Content` is the raw file contents of a `.md` file discovered from project and user directories (`.aimux/agents/`, `.claude/agents/`, `.codex/agents/`, `.claw/agents/`). These files are loaded without any content inspection. The user-supplied `prompt` is concatenated directly after the agent content.

An attacker who can place a file in any of the scanned agent directories (or who can influence the project working directory) can craft an agent `.md` file that prepends adversarial instructions to every prompt sent to the backing CLI. This is a **prompt injection** attack at the MCP layer.

Additionally, the `parseFrontmatter` function (`pkg/agents/registry.go:152`) extracts `role` from the YAML frontmatter without validating it against the known role set, allowing an agent file to override its role to any value (e.g., `role: coding` bypasses `readOnly=true`).

**Impact:** Prompt injection enabling instruction override in the backing AI CLI; role escalation (advisory role forced to non-read-only execution).

**Remediation:**
- Restrict agent discovery paths to project-local directories, not user home directory, or require explicit configuration opt-in.
- Validate the `role` field from frontmatter against the known role allowlist.
- Log and surface which agent files are loaded at startup for operator visibility.

---

### FIND-005 — Go runtime vulnerabilities: 7 confirmed CVEs (govulncheck)

**Tool:** `govulncheck ./...`
**Go version:** go1.25.4 (fix target: go1.25.8 for full remediation)

Seven vulnerabilities are confirmed as reachable by code paths in this module:

| ID | Package | Fixed in | Severity |
|----|---------|----------|----------|
| GO-2026-4602 | `os` — FileInfo escape from Root | go1.25.8 | High |
| GO-2026-4601 | `net/url` — IPv6 host literal parsing | go1.25.8 | High |
| GO-2026-4341 | `net/url` — memory exhaustion in query param parsing | go1.25.6 | High |
| GO-2026-4340 | `crypto/tls` — incorrect encryption level in handshake | go1.25.6 | High |
| GO-2026-4337 | `crypto/tls` — unexpected session resumption | go1.25.7 | High |
| GO-2025-4175 | `crypto/x509` — wildcard DNS constraint bypass | go1.25.5 | Medium |
| GO-2025-4155 | `crypto/x509` — excessive resource use on cert error | go1.25.5 | Medium |

The TLS and net/url vulnerabilities are reachable via the GenAI API client in `pkg/tools/deepresearch/client.go` on every `deepresearch` tool call. The `os.FileInfo` escape is reachable via `os.ReadDir` in `pkg/config/config.go:148`.

**Impact:** TLS session hijacking (GO-2026-4337/4340); memory exhaustion (GO-2026-4341); certificate validation bypass (GO-2025-4175).

**Remediation:** Upgrade Go toolchain to 1.25.8 or later and rebuild.

---

### FIND-006 — mergeEnv strips the parent process environment, leaking nothing but also stripping required OS environment

**File:** `pkg/executor/pipe/pipe.go:274–280`, `pkg/executor/conpty/conpty.go:66–70`

When `args.Env` is non-empty, the executors construct a replacement environment containing only the explicitly provided keys:

```go
func mergeEnv(extra map[string]string) []string {
    env := make([]string, 0)
    for k, v := range extra {
        env = append(env, k+"="+v)
    }
    return env  // parent environment NOT inherited
}
```

This is a double-edged issue:

1. **Security benefit:** The child process cannot inherit sensitive environment variables from the parent (e.g., `AWS_SECRET_ACCESS_KEY`, `GITHUB_TOKEN`). This is correct behavior if intentional.
2. **Security risk:** If `args.Env` is ever populated by a caller that includes user-controlled key=value pairs, those values are placed verbatim into the child environment without sanitization. Environment variable names containing `=` characters could corrupt the environment block.
3. **Operational risk:** Because parent env is stripped, if `args.Env` is sparse (e.g., only API keys), the child may lack `PATH`, `HOME`, and other OS essentials, causing silent failures that are hard to diagnose.

The current call sites in `handleExec` do not populate `args.Env`, so the code path that strips parent env is not reached in practice. However the interface is dangerous — any future caller that provides a partial `args.Env` will silently strip `PATH`.

**Impact:** Future callers risk spawning processes with incomplete environments; if user-supplied env key injection were added, it could corrupt process env blocks.

**Remediation:** Change `mergeEnv` to start from `os.Environ()` and overlay the extra keys. Add a key format validation (`strings.Contains(k, "=")` check) before inserting.

---

## P3 Medium

### FIND-007 — No prompt size limit; unbounded memory allocation for large prompts

**File:** `pkg/server/server.go:391` (handleExec)

Prompt size has no upper bound. The only size-related check is `StdinThreshold` (line 546), which routes large prompts to stdin piping — it does not reject oversized prompts. A caller can send a 100 MB prompt string that is buffered in memory, logged, injected into bootstrap templates, and passed to child processes via stdin.

**Impact:** Memory exhaustion; slow response; potential log file bloat.

**Remediation:** Reject prompts exceeding a configured `MaxPromptBytes` (e.g., 1 MB default) with a clear error before any processing.

---

### FIND-008 — API key stored in struct field accessible to error messages and logs

**File:** `pkg/tools/deepresearch/client.go:12`

```go
type Client struct {
    apiKey  string
    // ...
}
```

The `apiKey` field is unexported, which is correct. However, the `Client` struct can be accidentally logged (e.g., `%+v` formatting) exposing the key value. The error path at line 1047 of `server.go` includes the `clientErr` value in the MCP response:

```go
return mcp.NewToolResultError(fmt.Sprintf("DeepResearch unavailable: %v. Set GOOGLE_API_KEY or GEMINI_API_KEY.", clientErr)), nil
```

The `clientErr` here comes only from env var absence, not from key content — so this specific path is safe. But there is no structural protection preventing future error paths from inadvertently including the key.

**Impact:** Accidental API key exposure via verbose error messages or debug logging.

**Remediation:** Implement a `Redacted()` method or `String()` method on `Client` that returns `"[redacted]"`. Add a note in the struct definition warning against fmt-printing the struct.

---

### FIND-009 — Session ID and job ID enumeration possible via handleStatus

**File:** `pkg/server/server.go:654` (handleStatus)

`handleStatus` returns different errors for "job not found" vs. job metadata. An attacker can enumerate valid job IDs (UUIDs) by probing — a found job returns progress, content, and session_id; a missing job returns a 404-style error. While UUIDs (v7) are not guessable, the status endpoint exposes full job content including the CLI's output on completion:

```go
if j.Status == types.JobStatusCompleted || j.Status == types.JobStatusFailed {
    result["content"] = j.Content
```

There is no caller identity check — any MCP client connected to the server can read any job's output, regardless of who created it.

**Impact:** Cross-session data leakage in multi-tenant or shared MCP deployments.

**Remediation:** Associate jobs and sessions with a caller identity token (or at minimum a per-connection nonce) and validate it on status queries. In single-user deployments this is low risk, but the architecture should document the assumption.

---

### FIND-010 — AIMUX_CONFIG_DIR environment variable accepted without path validation

**File:** `cmd/aimux/main.go:63`, `pkg/orchestrator/pair.go:312`

```go
if dir := os.Getenv("AIMUX_CONFIG_DIR"); dir != "" {
    return dir
}
```

The config directory is accepted from the environment verbatim and used to load YAML profiles and prompt templates. No check ensures it is an absolute path or that it does not contain traversal components. An attacker controlling the environment (e.g., via a compromised parent process) can redirect config loading to a hostile directory.

**Impact:** Config hijacking; malicious CLI profiles loaded; arbitrary command specified via `command.base` in a profile YAML.

**Remediation:** Validate `AIMUX_CONFIG_DIR` with `filepath.Abs` + `os.Stat` + a check that the resolved path is within expected boundaries. Log a warning if the env-provided path differs from the binary-adjacent default.

---

## P4 Low

### FIND-011 — WAL and SQLite files created with world-readable permissions

**File:** `pkg/session/wal.go:34`, `pkg/session/sqlite.go:21`

Both files are created with mode `0o644`:

```go
f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
```

The WAL and SQLite database contain session metadata including `cwd` paths, CLI names, job content (which may include code or prompts), and error messages. On a multi-user system, other local users can read these files.

**Remediation:** Use `0o600` for both files. Consider `0o700` for the parent directory (`~/.config/aimux/`).

---

### FIND-012 — Log file created with world-readable permissions

**File:** `pkg/logger/logger.go:81`

```go
f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
```

Log entries include prompt lengths, session IDs, CLI names, error details, and job IDs. While prompt content is not logged, metadata leakage on multi-user systems is a risk.

**Remediation:** Use `0o600` for the log file.

---

### FIND-013 — Completion pattern regex compiled per-invocation without caching

**File:** `pkg/executor/pipe/pipe.go:69`

```go
re, reErr := regexp.Compile(args.CompletionPattern)
```

`CompletionPattern` comes from the CLI profile YAML. It is compiled on every `Run()` call. While the pattern is not user-supplied (it comes from config), repeated compilation under high load is inefficient and a future refactor that makes patterns user-configurable would create a ReDoS surface.

**Impact:** Minor performance overhead now; ReDoS risk if pattern ever becomes user-supplied.

**Remediation:** Cache compiled regexes keyed by pattern string in the executor or profile registry.

---

## Dependency CVE Summary

govulncheck confirmed 7 reachable stdlib vulnerabilities (see FIND-005). No third-party package CVEs were confirmed as reachable in current call paths, though 2 vulnerabilities exist in imported packages and 6 in required modules.

**Third-party dependencies of note:**
- `github.com/mark3labs/mcp-go v0.47.0` — no confirmed CVEs in this scan
- `google.golang.org/genai v1.52.1` — no confirmed CVEs in this scan
- `modernc.org/sqlite v1.48.1` — no confirmed CVEs in this scan
- `golang.org/x/crypto v0.36.0` — no CVEs found reachable
- `golang.org/x/net v0.38.0` — no CVEs found reachable

---

## Attack Surface Summary

| Vector | Exposure |
|--------|----------|
| MCP protocol (stdio) | All 11 tool handlers receive user input |
| `prompt` parameter | Passed as CLI arg or stdin; no size limit |
| `cwd` parameter | Passed as `cmd.Dir` without validation |
| `model` parameter | Passed as CLI flag value without allowlist |
| `reasoning_effort` parameter | Passed as CLI flag value without allowlist |
| Agent content | Read from disk, prepended to user prompt |
| `AIMUX_CONFIG_DIR` | Accepted from env without path validation |
| Environment | Child processes get stripped env (no parent inheritance) |
| API key | Read from env; unexported but no `String()` guard |

---

## Remediation Priority Order

1. **FIND-001** (P1) — Enforce `MaxConcurrentJobs` before spawning goroutines
2. **FIND-005** (P2) — Upgrade Go to 1.25.8 (covers 7 CVEs in one step)
3. **FIND-003** (P2) — Add model/effort allowlist validation; fix `fmt.Sprintf` template mismatch
4. **FIND-002** (P2) — Validate and clean `cwd` parameter before use as `cmd.Dir`
5. **FIND-004** (P2) — Restrict agent discovery paths; validate role from frontmatter
6. **FIND-006** (P2) — Fix `mergeEnv` to inherit parent env; add key format validation
7. **FIND-007** (P3) — Add `MaxPromptBytes` check in `handleExec`
8. **FIND-010** (P3) — Validate `AIMUX_CONFIG_DIR` path before use
9. **FIND-008** (P3) — Add `String()` / `Redacted()` to `Client`
10. **FIND-009** (P3) — Document single-user assumption; add caller identity for multi-tenant use
11. **FIND-011/012** (P4) — Change file permissions to `0o600`
12. **FIND-013** (P4) — Cache compiled completion pattern regexes

---

*Report generated by security-scan-specialist. This is a read-only analysis — no files were modified.*
