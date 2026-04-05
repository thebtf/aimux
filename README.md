# aimux — MCP Server for Multi-CLI AI Orchestration

Single Go binary that multiplexes 7 AI coding CLIs (codex, gemini, claude, qwen, aider, droid, opencode) via the Model Context Protocol (MCP).

## Features

- **10 MCP tools**: exec, status, sessions, audit, think, investigate, consensus, debate, dialog, agents
- **Mandatory pair coding**: Every `exec(role="coding")` runs driver + reviewer (Constitution P2)
- **5 orchestration strategies**: PairCoding, SequentialDialog, ParallelConsensus, StructuredDebate, AuditPipeline
- **3 executor backends**: ConPTY (Windows), PTY (Linux/Mac), Pipe (fallback)
- **Circuit breakers** with exponential backoff per CLI
- **WAL + SQLite** persistence with crash recovery
- **Composable prompt templates** with includes and output styles
- **Agent registry** with 9-source discovery

## Installation

```bash
# From source
go install github.com/thebtf/aimux/cmd/aimux@latest

# Or download binary
# See Releases for Windows/Linux/macOS (amd64 + arm64)
```

## Quick Start

Add to your MCP client config (e.g., Claude Code `settings.json`):

```json
{
  "mcpServers": {
    "aimux": {
      "command": "aimux",
      "env": {
        "AIMUX_CONFIG_DIR": "/path/to/config"
      }
    }
  }
}
```

## Configuration

### Server Config (`config/default.yaml`)

```yaml
server:
  log_level: info
  log_file: ~/.config/aimux/aimux.log
  db_path: ~/.config/aimux/sessions.db
  default_timeout_seconds: 300

  audit:
    scanner_role: codereview
    validator_role: analyze
    default_mode: standard

  pair:
    driver_role: coding
    reviewer_role: codereview
    max_rounds: 3
```

### CLI Profiles (`config/cli.d/{name}/profile.yaml`)

Each CLI is a self-contained plugin directory. Adding a new CLI = drop a YAML file:

```yaml
name: codex
binary: codex
features:
  streaming: true
  headless: true
  read_only: true
  session_resume: true
  jsonl: true
timeout_seconds: 3600
```

### Role Routing

Roles auto-resolve to CLIs via preferences. Override with environment:

```bash
export AIMUX_ROLE_CODING=codex:gpt-5.3-codex:medium
export AIMUX_ROLE_THINKDEEP=codex:gpt-5.4:high
```

## Architecture

```
cmd/aimux/          Entry point, signal handling
pkg/types/          Shared types, interfaces, typed errors
pkg/config/         YAML parsing, CLI profile discovery
pkg/routing/        Role → CLI resolution
pkg/executor/       ConPTY/PTY/Pipe process spawning
pkg/session/        Registry, jobs, WAL, SQLite, GC
pkg/server/         MCP server, 10 tool handlers
pkg/orchestrator/   5 strategies (pair, dialog, consensus, debate, audit)
pkg/parser/         JSONL, JSON, text output parsers
pkg/prompt/         Composable template engine
pkg/agents/         Agent registry with multi-source discovery
pkg/logger/         Async file logger
pkg/driver/         CLI registry, command templates
config/             YAML configs, CLI profiles, prompt templates
```

## MCP Tools

| Tool | Description |
|------|-------------|
| `exec` | Execute prompt via AI CLI with role routing |
| `status` | Check async job status |
| `sessions` | Manage sessions: list, info, health, cancel |
| `audit` | Multi-agent codebase audit (quick/standard/deep) |
| `think` | Structured thinking patterns (17 patterns) |
| `investigate` | Iterative convergent investigation |
| `consensus` | Blinded parallel multi-model opinions |
| `debate` | Adversarial structured debate |
| `dialog` | Sequential multi-turn dialog |
| `agents` | Discover and run Loom Agents |

## Development

```bash
# Build
go build ./cmd/aimux/

# Test
go test ./...

# Test with race detector (Linux/macOS)
go test -race ./...

# Cross-compile
GOOS=linux GOARCH=amd64 go build -o dist/aimux-linux-amd64 ./cmd/aimux/
```

## License

MIT
