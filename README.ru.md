<!-- synced: 2026-05-06 source-commit: dc83067 -->
[English](README.md) | [Русский](README.ru.md)

# aimux

[![Go](https://img.shields.io/badge/go-1.25.9%2B-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![License](https://img.shields.io/badge/license-MIT-blue)](LICENSE)
[![MCP Tools](https://img.shields.io/badge/MCP-27%20tools-blueviolet)](https://modelcontextprotocol.io)

aimux — MCP-сервер для устойчивого состояния задач, операций с сессиями,
глубокого исследования, обновления бинарника и caller-centered structured
reasoning.

Текущая live surface после purge намеренно небольшая:

- 4 server tools: `status`, `sessions`, `deepresearch`, `upgrade`
- 1 caller-centered `think` harness и 22 cognitive move tools

Прежние CLI-launching MCP tools (`exec`, `agent`, `agents`, `critique`,
`investigate`, `consensus`, `debate`, `dialog`, `audit`, `workflow`) удалены из
live surface. Их pre-purge архитектура заморожена в ветке
`snapshot/v5.0.3-pre-cli-purge` и описана в
[docs/architecture/cli-tools-current.md](docs/architecture/cli-tools-current.md).
Следующая Layer 5 surface отслеживается отдельно в AIMUX-9 / DEF-1.

## Быстрый старт

### Сборка

```powershell
$env:GOTOOLCHAIN = "go1.25.9"
go build -o aimux.exe ./cmd/aimux/
.\aimux.exe --version
```

Для production-сборок используйте Go 1.25.9 или новее.

### Подключение MCP client

Добавьте бинарник в конфигурацию MCP client:

```json
{
  "mcpServers": {
    "aimux": {
      "command": "D:/Dev/aimux/aimux.exe",
      "args": []
    }
  }
}
```

### Проверка tool surface

Вызовите `tools/list` из любого MCP-capable client. Актуальная сборка должна
показывать 27 tools: 4 server tools, `think` harness и 22 cognitive move tools.

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "tools/list",
  "params": {}
}
```

## Команды

Обычные development и release checks:

```powershell
$env:GOTOOLCHAIN = "go1.25.9"
go build ./...
go test ./... -count=1 -timeout 300s
go test -tags=critical ./tests/critical/... -count=1 -timeout 300s
go vet ./...
go mod verify
govulncheck ./...

Set-Location loom
go test ./... -count=1
```

Для customer-mode release walkthrough используйте
[docs/PRODUCTION-TESTING-PLAYBOOK.md](docs/PRODUCTION-TESTING-PLAYBOOK.md).

## MCP Tool Reference

### Server Tools

| Tool | Назначение |
|---|---|
| `status` | Запрос статуса async job/task. |
| `sessions` | Просмотр, инспекция, отмена, kill, garbage collection и health-check состояния сессий и задач. |
| `deepresearch` | Gemini-backed исследование со structured output. |
| `upgrade` | Проверка или применение обновлений aimux binary, включая local source install с честным deferred fallback. |

### Think Harness

`think(action=start|step|finalize)` — canonical caller-centered thinking
harness. Caller владеет final answer; aimux отслеживает visible work products,
evidence, gate status, confidence ceilings, unresolved objections, budget state
и bounded `trace_summary`.

Типичный flow:

1. `think(action=start, task=..., context_summary=...)` создаёт session и
   возвращает allowed cognitive moves плюс первый prompt.
2. `think(action=step, session_id=..., chosen_move=..., work_product=...,
   evidence=[...], confidence=...)` записывает visible move result и возвращает
   gate/confidence feedback.
3. `think(action=finalize, session_id=..., proposed_answer=...)` принимает
   ответ только когда loop, evidence, confidence, objections и budget gates его
   поддерживают.

Legacy `think(thought=...)` calls fail closed с migration error. Они не делают
keyword routing, не создают implicit sessions и не возвращают pattern
suggestion fields.

### Cognitive Move Tools

22 cognitive move tools дают in-process structured reasoning moves. Они не
запускают AI CLIs.

| Tool | Использование |
|---|---|
| `architecture_analysis` | Архитектурные tradeoffs и структура системы. |
| `collaborative_reasoning` | Синтез нескольких перспектив. |
| `critical_thinking` | Adversarial review плана или утверждения. |
| `debugging_approach` | Планирование debug hypotheses. |
| `decision_framework` | Анализ tradeoffs и decision records. |
| `domain_modeling` | Domain concepts, boundaries и language. |
| `experimental_loop` | Итерация experiments и observations. |
| `literature_review` | Сравнение sources и findings. |
| `mental_model` | Объяснение или построение conceptual models. |
| `metacognitive_monitoring` | Проверка reasoning quality и confidence. |
| `peer_review` | Review artifact с позиции reviewer. |
| `problem_decomposition` | Разбиение сложной работы на tractable parts. |
| `recursive_thinking` | Повторная проверка выводов на нескольких уровнях. |
| `replication_analysis` | Оценка reproducibility и недостающих evidence. |
| `research_synthesis` | Объединение research evidence в выводы. |
| `scientific_method` | Hypothesis, experiment, observation, conclusion. |
| `sequential_thinking` | Последовательное step-by-step reasoning. |
| `source_comparison` | Сравнение claims across sources. |
| `stochastic_algorithm` | Разбор randomized или probabilistic approaches. |
| `structured_argumentation` | Claims, evidence, objections и rebuttals. |
| `temporal_thinking` | Timeline, sequencing и time-based effects. |
| `visual_reasoning` | Spatial или visual structure reasoning. |

Каждый per-pattern result включает gate status и advisor recommendation.
Stateless calls возвращают `gate_status: "complete"`; stateful pattern sessions
могут запросить дополнительные шаги, если gate видит missing evidence или
недостаточную глубину reasoning.

## Обзор архитектуры

```mermaid
flowchart TD
    Client[MCP client] --> Server[aimux MCP server]
    Server --> Budget[response budget layer]
    Budget --> Sessions[sessions/status handlers]
    Budget --> Research[deepresearch handler]
    Budget --> Upgrade[upgrade handler]
    Budget --> Think[think harness and cognitive move handlers]

    Sessions --> Loom[LoomEngine]
    Loom --> SQLite[(SQLite task/session state)]
    Research --> Gemini[Gemini SDK]
    Think --> Gates[pattern gates and advisor]
    Upgrade --> Binary[local or release binary swap]
```

### Loom — canonical runtime state

Loom — canonical runtime job/task state backend. Legacy JobManager runtime
backend удалён. Public session/status responses читают состояние задач из Loom
и legacy session metadata там, где это нужно для migration visibility.

Loom engine также является standalone nested Go module:

- Module path: `github.com/thebtf/aimux/loom`
- Consumer guide: [loom/USAGE.md](loom/USAGE.md)
- Contract: [loom/CONTRACT.md](loom/CONTRACT.md)
- Recovery guide: [loom/RECOVERY.md](loom/RECOVERY.md)

## Структура репозитория

| Path | Назначение |
|---|---|
| `cmd/aimux/` | Server entry point и binary wiring. |
| `pkg/server/` | MCP tool registration, handlers, response budgeting и transport wiring. |
| `pkg/think/` | Think pattern execution, gates и advisor. |
| `pkg/tools/deepresearch/` | Gemini-backed deep research. |
| `pkg/upgrade/`, `pkg/updater/` | Binary update, local source install и handoff/deferred coordination. |
| `pkg/session/` | Session metadata store. |
| `loom/` | Standalone durable task engine module. |
| `tests/critical/` | Release-blocking critical suite. |
| `docs/` | Public architecture и production testing documentation. |

## Текущий scope и roadmap

Current production surface:

- Session и task health/status operations.
- Deep research через Gemini SDK.
- Binary update с local source install и deferred fallback, когда live handoff не поддержан.
- Caller-centered `think` harness и 22 local cognitive move tools.
- Loom-backed task state и recovery.

Out of current scope:

- Direct CLI execution over MCP.
- Agent registry execution over MCP.
- Multi-model orchestration tools over MCP.
- Pipeline v5 Layer 5 exposure.

Эти удалённые surfaces не являются runtime defects текущей сборки. Это future
design work в AIMUX-9 / DEF-1.

## Release gates

Перед release:

1. Собрать с Go 1.25.9 или новее.
2. Запустить полный Go test suite.
3. Запустить critical suite в `tests/critical/`.
4. Запустить `go vet`, `go mod verify` и `govulncheck`.
5. Пройти [docs/PRODUCTION-TESTING-PLAYBOOK.md](docs/PRODUCTION-TESTING-PLAYBOOK.md)
   в customer mode.
6. Проверить freshness установленного/running binary через `upgrade(action="check")`.
7. Проверить local-source install через MCP client или `mcp-launcher -mode install`.

## License

MIT. См. [LICENSE](LICENSE).
