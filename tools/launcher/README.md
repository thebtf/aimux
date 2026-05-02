# launcher — aimux executor stack debug tool

## What this is

Standalone debug binary that decorates `types.ExecutorV2` above CLI subprocesses
(ConPTY/PTY/Pipe) and HTTP API (OpenAI/Anthropic/Google) backends. Built to
investigate executor regressions without rebuilding the aimux daemon.

Two debug levels:

- **L1 (default)**: wraps any `ExecutorV2`, emits structured JSONL events
  (spawn_args, complete, classify, breaker_state, cooldown_state).
- **L2 (`--bypass`)**: pipe-only raw spawn; inserts `io.TeeReader` before
  `IOManager` so raw bytes pre-StripANSI reach the JSONL log.

> **NFR-7 warning — secrets in `--bypass --log`**
>
> `--bypass --log <path>` writes raw subprocess bytes **UNREDACTED** to disk.
> The log file MAY contain API tokens, passwords, or other secrets pasted in
> `--prompt` or echoed by the backend. Use only in a trusted dev environment.
> Delete the log after debugging: `rm <path>`

## Build

```bash
go build ./tools/launcher/
# produces launcher.exe (Windows) or ./launcher (Linux/macOS)
```

## Subcommands

### launcher cli — one-shot CLI prompt

```
launcher cli [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--cli <name>` | required | CLI name matching a profile in `config/cli.d/` |
| `--prompt <text>` | required | Prompt text to send |
| `--model <m>` | profile default | Model override |
| `--effort <e>` | profile default | Reasoning effort (low/medium/high) |
| `--cwd <dir>` | inherit | Working directory for the spawned process |
| `--executor <e>` | `pipe` | Backend: `pipe\|conpty\|pty\|auto` |
| `--log <path>` | none | Append JSONL events to this file |
| `--bypass` | false | L2 mode: raw bytes pre-StripANSI (pipe only) |
| `--diag` | false | Emit per-chunk diagnostic events to the JSONL log (pipe only) |
| `--stream` | false | Use `SendStream` (streaming chunks) |
| `--config-dir <d>` | `config` | aimux config directory |

Examples:

```bash
launcher cli --cli codex --prompt "echo test"
launcher cli --cli codex --prompt "echo test" --log /tmp/debug.jsonl
launcher cli --cli codex --prompt "echo test" --bypass --log /tmp/raw.jsonl
launcher cli --cli codex --prompt "echo test" --stream
```

### launcher api — one-shot HTTP API prompt

```
launcher api [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--provider <p>` | required | `openai\|anthropic\|google` |
| `--model <m>` | provider default | Model name |
| `--prompt <text>` | required | Prompt text |
| `--api-key-env <var>` | per provider | Env var name holding the API key |
| `--stream` | false | Use `SendStream` |
| `--log <path>` | none | Append JSONL events |

Default key env vars: `OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, `GOOGLE_AI_API_KEY`.

Examples:

```bash
launcher api --provider openai --model gpt-4o-mini --prompt "say hi"
launcher api --provider anthropic --prompt "hello" --stream --log out.jsonl
```

### launcher session — interactive REPL

```
launcher session [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--cli <name>` | | CLI session (mutually exclusive with `--provider`) |
| `--provider <p>` | | API provider (mutually exclusive with `--cli`) |
| `--model <m>` | profile default | Model override |
| `--executor <e>` | `pipe` | Backend (CLI mode only): `pipe\|conpty\|pty\|auto` |
| `--cwd <dir>` | inherit | Working directory |
| `--log <path>` | none | Append JSONL events |
| `--config-dir <d>` | `config` | aimux config directory |
| `--diag` | — | Not applicable (CLI `--cli` only; see `launcher cli --diag`) |

**Backend mode determines the loop:**

| `--executor` | Loop | Best for |
|---|---|---|
| `pipe` (default) | Request/response REPL — sends prompt, waits for full response, emits `turn{user}`/`turn{agent}` events | Headless CLIs: codex `--full-auto --json -`, claude `--print`, gemini `-p` |
| `conpty` | Bidirectional interactive passthrough — bytes flow in both directions as they arrive; ANSI escape sequences pass through so the CLI's TUI renders in the operator's terminal | Gemini TUI, codex chat, aider's REPL on Windows |
| `pty` | Same as `conpty` (Unix PTY variant) | Same as conpty, on Linux/macOS |

**Slash-commands — pipe (request/response) mode:**

| Command | Description |
|---------|-------------|
| `/quit` | Close session and exit 0 |
| `/reset` | Close and restart the session (CLI only) |
| `/dump` | Print current breaker / cooldown / classify state |
| `/save <path>` | Snapshot the current log to `<path>` |
| `/raw on\|off` | Toggle L2 raw tee (pipe CLI sessions only) |
| `/history` | Print conversation turns |
| `/help` | List slash-commands |

**Slash-commands — conpty/pty (interactive TUI) mode:**

| Command | Description |
|---------|-------------|
| `/quit` | Close session and exit 0 |
| `/help` | List slash-commands |

All other slash-commands are not available in interactive mode (they require the
request/response model to track conversation state).  Use the CLI's own keybindings
for navigation within the TUI.

Examples:

```bash
# Pipe backend: request/response REPL (headless CLIs, codex/claude/gemini -p flags)
launcher session --cli codex
launcher session --cli codex --log /tmp/sess.jsonl
echo "hello" | launcher session --cli codex   # non-interactive via piped stdin

# ConPTY backend: interactive TUI passthrough (gemini full TUI, aider, codex chat)
launcher session --cli gemini --executor conpty --log /tmp/gemini-tui.jsonl
# Operator sees gemini's full TUI rendered; types prompts directly; /quit to exit.
# Log captures raw bytes as bytes_hex (NFR-7: may contain terminal escapes/secrets).
```

### launcher validate — run CR-002 validation harness

```
launcher validate [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--out <dir>` | `.agent/reports` | Directory for timestamped markdown reports and run logs |
| `--config-dir <dir>` | `config` | Live aimux config directory for real CLI scenarios |
| `--cli-scope <list>` | `codex,claude,gemini` | Comma-separated real CLI scope |
| `--include-api` | `true` | Run API provider gates when credentials are present |
| `--include-manual` | `true` | Include the manual TUI evidence recipe in the report |
| `--synthetic-only` | `false` | Run deterministic synthetic ANSI and large-output scenarios only |
| `--allow-blocked` | `false` | Exit 0 when scenarios are BLOCKED and no scenario FAILs |
| `--timeout <duration>` | `30s` | Per-scenario timeout |

The harness writes `AIMUX-17-cr002-validation-<timestamp>.md` under `--out`. **FAIL** returns exit code 1. **BLOCKED** returns exit code 2 by default because an external prerequisite prevented validation, such as a missing binary, missing API key, unavailable network, quota/rate-limit, or model-scope denial. Use `--allow-blocked` only for advisory inventory runs where BLOCKED scenarios must not fail the process.

Synthetic scenarios build temporary fixture binaries under the run directory and generate a temporary config directory from `tools/launcher/testdata/emitters/`; they never create or require live profiles under `config/cli.d/`. The ANSI scenario proves raw `bytes_hex` contains `1b5b` while the paired line output is stripped. The large-output scenario emits at least 50 MB and records output size, log size, and memory evidence availability.

Examples:

```bash
launcher validate --out .agent/reports --synthetic-only
launcher validate --out .agent/reports --cli-scope codex,claude,gemini --timeout 30s
launcher validate --out .agent/reports --include-api=false --include-manual=false
```

Full TUI automation is intentionally a manual gate because stable terminal injection is brittle on Windows. The generated report includes exact `launcher session --executor conpty` or `--executor pty` steps, `/help` and `/quit` checks for the interactive path, the automated pipe-session `/dump` evidence reference, log path, and artifact checklist.

### launcher replay — read a captured JSONL log

```
launcher replay [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--log <path>` | required | JSONL log file to read |
| `--filter <kinds>` | all | Comma-separated list of kinds to include |
| `--raw` | false | Re-emit raw NDJSON byte-identical to input |

Examples:

```bash
launcher replay --log /tmp/debug.jsonl
launcher replay --log /tmp/debug.jsonl --filter stdout,stderr
launcher replay --log /tmp/debug.jsonl --raw | head -5
```

## L1 vs L2 capability matrix

| Capability | L1 (default) | L2 (`--bypass`, pipe only) |
|------------|--------------|---------------------------|
| Works with CLI executors | yes (pipe/conpty/pty) | pipe only |
| Works with API executors | yes | no |
| `spawn_args` event | yes | yes |
| `complete` event | yes | no (bypass skips decorator) |
| `classify` event | yes | no |
| `breaker_state` / `cooldown_state` | yes | no |
| `stdout` / `stderr` stream events | no | yes |
| Raw bytes pre-StripANSI (`stream:"raw"`) | no | yes |
| ANSI-stripped lines (`stream:"line"`) | no | yes |
| Streaming `chunk` events | yes (SendStream) | no |
| ANSI stripped in response | yes (via IOManager) | side-by-side (raw + line) |
| Secrets redacted | yes | **NO** — unredacted by design |

## 8 verification smoke commands

```bash
# 1. One-shot CLI (codex)
launcher cli --cli codex --prompt "echo ok"

# 2. One-shot API (requires OPENAI_API_KEY)
launcher api --provider openai --model gpt-4o-mini --api-key-env OPENAI_API_KEY --prompt "say hi"

# 3. CLI session (interactive)
launcher session --cli codex

# 4. API session (interactive — requires OPENAI_API_KEY)
launcher session --provider openai --model gpt-4o-mini

# 5. CLI streaming
launcher cli --cli codex --prompt "echo test" --stream

# 6. API streaming (requires OPENAI_API_KEY)
launcher api --provider openai --model gpt-4o-mini --prompt "hello" --stream

# 7. L2 bypass capture + replay (automatable)
launcher cli --cli codex --prompt "echo test" --bypass --log /tmp/raw.jsonl
launcher replay --log /tmp/raw.jsonl --filter stdout

# 8. Breaker opens after repeated failures
#    Covered by TestDebugExecutor_Send_ErrorPath in debug_executor_test.go
```

## Troubleshooting

| Symptom | Cause | Fix |
|---------|-------|-----|
| Headless CLI hangs or waits for TTY input under ConPTY | Misuse: headless mode + ConPTY is the wrong combination. The CLI sees a TTY, renders its TUI, and waits for operator input. | Use `--executor pipe` (default) for headless/JSON modes; use `--executor conpty` only for interactive TUI sessions |
| Interactive TUI session exits immediately with `[EOF — closing session]` | Operator stdin EOF reaching the pipe-mode REPL before the TUI renders | Switch to `--executor conpty` (or `--executor pty` on Linux/macOS) — the interactive loop keeps the session alive until /quit |
| `launcher: env var OPENAI_API_KEY empty` | API key not set | `export OPENAI_API_KEY=<key>` |
| `--bypass` with PTY/ConPTY fails | L2 is pipe-only | Add `--executor pipe` |
| `launcher session` returns "not implemented" | API executor lacks SessionFactory | Use `launcher api` instead |
| Partial last line in replay | Process killed mid-write | Normal — replay discards partial lines with a warning |
| Log file grows without bound | No rotation built in | Delete or archive manually after debugging |

### ConPTY/PTY: right tool, right mode

ConPTY (Windows) and PTY (Linux/macOS) create a real pseudo-terminal so that
the child process sees `stdin`/`stdout` as a TTY (`isatty() == true`). This is
what enables real interactive sessions: gemini TUI, codex chat, aider's REPL.

**Headless flags + ConPTY = misuse.** Verified 2026-05-01 with `--diag` mode:

| CLI | Headless command | ConPTY behaviour | Pipe behaviour |
|-----|------------------|------------------|----------------|
| `codex exec --full-auto --json -` | sees TTY → silently waits for interactive input (deprecation warning at +0.1s, then idle until kill) | streams JSONL events, exits 0 in ~10s |
| `claude --print` | exits 1 immediately with `Error: Input must be provided either through stdin or as a prompt argument when using --print` | streams result JSONL, exits 0 in ~12s |
| `gemini -p` | renders full TUI (header bar, status bar, prompt input) and waits for typing | streams JSONL events, exits 0 in ~10s |

**Rule of thumb:**

- Headless/JSON/automation flags (`--full-auto --json -`, `--print`, `-p`) → `--executor pipe` (default)
- Interactive TUI / chat sessions → `--executor conpty` (Windows) or `--executor pty` (Linux/macOS)

To reproduce the diagnostic:

```powershell
# Headless via pipe (correct)
launcher cli --cli gemini --prompt "say ok" --diag

# Same prompt via ConPTY — observe TUI render and wait for input (intentional for interactive use)
launcher cli --cli gemini --prompt "say ok" --executor conpty --diag
```

For a full interactive session with gemini's TUI:

```powershell
launcher session --cli gemini --executor conpty
# Operator sees gemini's header bar, status bar, and prompt input in real time.
# Type prompts directly; /quit to close.
```
