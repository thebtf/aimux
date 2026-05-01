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
| `--executor <e>` | `pipe` | Backend (CLI mode only) |
| `--cwd <dir>` | inherit | Working directory |
| `--log <path>` | none | Append JSONL events |
| `--config-dir <d>` | `config` | aimux config directory |

**Slash-commands:**

| Command | Description |
|---------|-------------|
| `/quit` | Close session and exit 0 |
| `/reset` | Close and restart the session (CLI only) |
| `/dump` | Print current breaker / cooldown / classify state |
| `/save <path>` | Snapshot the current log to `<path>` |
| `/raw on\|off` | Toggle L2 raw tee (pipe CLI sessions only) |
| `/history` | Print conversation turns |
| `/help` | List slash-commands |

Examples:

```bash
launcher session --cli codex
launcher session --cli codex --log /tmp/sess.jsonl
echo "hello" | launcher session --cli codex   # non-interactive via piped stdin
```

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
| Headless CLI hangs / errors under ConPTY | TTY detection — see "ConPTY ≠ headless" below | Use `--executor pipe` (default) |
| `launcher: env var OPENAI_API_KEY empty` | API key not set | `export OPENAI_API_KEY=<key>` |
| `--bypass` with PTY/ConPTY fails | L2 is pipe-only | Add `--executor pipe` |
| `launcher session` returns "not implemented" | API executor lacks SessionFactory | Use `launcher api` instead |
| Partial last line in replay | Process killed mid-write | Normal — replay discards partial lines with a warning |
| Log file grows without bound | No rotation built in | Delete or archive manually after debugging |

### ConPTY ≠ headless: TTY detection in headless CLIs

ConPTY backend creates a Windows pseudo-terminal so that the child process sees
`stdin`/`stdout` as a TTY (`isatty() == true`). This is what enables real
interactive sessions: codex chat, gemini TUI, aider's REPL.

**Headless modes detect the TTY and refuse / change behaviour.** Verified
2026-05-01 with `--diag` mode (each line shown with timestamp):

| CLI | Headless command | ConPTY behaviour | Pipe behaviour |
|-----|------------------|------------------|----------------|
| `codex exec --full-auto --json -` | sees TTY → silently waits for interactive input (deprecation warning at +0.1s, then idle until kill) | streams JSONL events, exits 0 in ~10s |
| `claude --print` | exits 1 immediately with `Error: Input must be provided either through stdin or as a prompt argument when using --print` (it refuses to read TTY as stdin) | streams result JSONL, exits 0 in ~12s |
| `gemini -p` | renders full TUI (header bar, status bar, prompt input) and waits for typing | streams JSONL events, exits 0 in ~10s |

**Conclusion:** ConPTY is not broken — it correctly delivers a TTY. Headless
JSON modes are designed for pipe stdin and break under TTY by design. The
launcher's default `--executor pipe` is the right choice for any
`--full-auto / --json / --print / -p` flow. Reach for `--executor conpty` only
when testing real interactive TUI / chat sessions.

To reproduce the diagnostic yourself:

```powershell
# Headless via pipe (works)
launcher cli --cli gemini --prompt "say ok" --diag

# Same prompt via ConPTY — observe what really happens, in real time
launcher cli --cli gemini --prompt "say ok" --executor conpty --diag
```
