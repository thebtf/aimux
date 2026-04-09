# Implementation Plan: Skill Engine

**Spec:** .agent/specs/skill-engine/spec.md
**Created:** 2026-04-08
**Status:** Draft

> **Provenance:** Planned by claude-opus-4-6 on 2026-04-08.
> Evidence from: spec.md (12 FR, 6 NFR), codebase analysis (pkg/prompt/, pkg/server/,
> config/cli.d/, pkg/think/patterns/), research corpus (72 patterns).
> Confidence: VERIFIED (existing code patterns) / INFERRED (new architecture).

## Tech Stack

| Component | Choice | Rationale |
|-----------|--------|-----------|
| Template engine | Go `text/template` | stdlib, supports conditionals, range, nested templates natively |
| Frontmatter parsing | `gopkg.in/yaml.v3` | already in go.mod (used by config/) |
| Embedding | `embed` (stdlib) | single-binary, matches existing pattern |
| Caller discovery | `os.Stat` + `filepath.Glob` | no external deps, scan CWD on connect |

## Architecture

```
                      ┌─────────────────┐
                      │   MCP Client    │
                      │ (Claude/Codex)  │
                      └────────┬────────┘
                               │ GetPrompt("aimux-debug", {error: "..."})
                               ▼
┌──────────────────────────────────────────────────────┐
│                   pkg/server/                         │
│  registerPrompts() ──→ skill-based auto-registration │
│  handleSkillPrompt() ──→ skills.Render(name, data)   │
└──────────────────┬───────────────────────────────────┘
                   │
                   ▼
┌──────────────────────────────────────────────────────┐
│                   pkg/skills/                         │
│                                                      │
│  Engine                                              │
│  ├── Load(embeddedFS, diskPath)                      │
│  │   ├── parse frontmatter (yaml)                    │
│  │   ├── parse Go templates (text/template)          │
│  │   ├── load fragments (_fragments/*.md)            │
│  │   ├── build bidirectional graph                   │
│  │   └── validate against _map.yaml                  │
│  │                                                   │
│  ├── Render(name, SkillData) → string                │
│  │   ├── inject live data (CLIs, metrics, reports)   │
│  │   ├── inject caller skills (discovered)           │
│  │   ├── inject related skills (bidirectional)       │
│  │   ├── execute template → markdown output          │
│  │   └── recover() on panic → graceful error         │
│  │                                                   │
│  ├── Skills() → []SkillMeta (for auto-registration)  │
│  └── ValidateMap() → []Warning                       │
│                                                      │
│  SkillData struct                                    │
│  ├── EnabledCLIs, CLICount, HasMultipleCLIs          │
│  ├── RoleRouting map[string]string                   │
│  ├── Metrics MetricsSnapshot                         │
│  ├── PastReports []ReportInfo                        │
│  ├── Agents []AgentMeta                              │
│  ├── ThinkPatterns []string                          │
│  ├── CallerSkills []string                           │
│  ├── RelatedSkills []SkillMeta                       │
│  ├── Args map[string]string (from MCP request)       │
│  └── CallerHasSkill(name) bool (template func)       │
│                                                      │
└──────────────────────────────────────────────────────┘
                   │
                   │ loads from
                   ▼
┌──────────────────────────────────────────────────────┐
│              config/skills.d/                         │
│  ├── _map.yaml          (graph map — source of truth)│
│  ├── _fragments/        (shared template fragments)  │
│  │   ├── evidence-table.md                           │
│  │   ├── verification-gate.md                        │
│  │   ├── delegation-tree.md                          │
│  │   ├── priority-scoring.md                         │
│  │   └── integrity-commandments.md                   │
│  ├── debug.md           (skill template)             │
│  ├── review.md                                       │
│  ├── audit.md                                        │
│  ├── research.md                                     │
│  ├── consensus.md                                    │
│  ├── investigate.md                                  │
│  ├── guide.md                                        │
│  ├── workflow.md                                     │
│  ├── background.md                                   │
│  ├── agent-exec.md                                   │
│  ├── security.md                                     │
│  ├── delegate.md                                     │
│  └── tdd.md                                          │
└──────────────────────────────────────────────────────┘
```

## Data Model

### SkillMeta (parsed from frontmatter)
| Field | Type | Source |
|-------|------|--------|
| Name | string | frontmatter `name:` |
| Description | string | frontmatter `description:` |
| Prompt | bool | frontmatter `prompt:` (default true) |
| Args | []ArgDef | frontmatter `args:` |
| Related | []string | frontmatter `related:` |
| Tags | []string | frontmatter `tags:` |
| FilePath | string | computed on load |
| IsFragment | bool | computed (underscore prefix or `prompt: false`) |

### ArgDef
| Field | Type |
|-------|------|
| Name | string |
| Description | string |

### SkillData (runtime data for template rendering)
| Field | Type | Source |
|-------|------|--------|
| EnabledCLIs | []string | registry.EnabledCLIs() |
| CLICount | int | len(EnabledCLIs) |
| HasMultipleCLIs | bool | CLICount >= 2 |
| HasGemini | bool | "gemini" in EnabledCLIs |
| RoleRouting | map[string]string | router.Resolve() per role |
| Metrics | MetricsSnapshot | metrics.Snapshot() |
| PastReports | []ReportInfo | investigate.ListReports() |
| Agents | []AgentMeta | agentReg.List() summaries |
| ThinkPatterns | []string | think.GetAllPatterns() |
| CallerSkills | []string | discovered from CWD |
| RelatedSkills | []SkillMeta | computed from graph |
| Args | map[string]string | from MCP GetPromptRequest |

### Template Functions (registered via FuncMap)
| Function | Signature | Purpose |
|----------|-----------|---------|
| CallerHasSkill | (name string) bool | Check if caller has a skill |
| JoinCLIs | () string | strings.Join(EnabledCLIs, ", ") |
| RoleFor | (role string) string | Get CLI name for a role |

## API Contracts

### pkg/skills.Engine

```go
type Engine struct { ... }

func NewEngine() *Engine
func (e *Engine) Load(embedded embed.FS, diskDir string) error
func (e *Engine) Render(name string, data *SkillData) (string, error)
func (e *Engine) Skills() []SkillMeta       // all prompt-registered skills
func (e *Engine) Get(name string) *SkillMeta // nil if not found
func (e *Engine) ValidateMap() []string      // warnings
```

### pkg/server integration

```go
// In NewMCPServer(), after existing registerPrompts():
for _, sk := range s.skillEngine.Skills() {
    s.registerSkillPrompt(sk)
}

// Generic handler for all skill-based prompts:
func (s *Server) handleSkillPrompt(name string) mcp.PromptHandlerFunc {
    return func(_ context.Context, req mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
        data := s.buildSkillData(req)
        content, err := s.skillEngine.Render(name, data)
        if err != nil {
            return mcp.NewToolResultError(err.Error()), nil
        }
        return mcp.NewGetPromptResult(
            fmt.Sprintf("aimux-%s", name),
            []mcp.PromptMessage{mcp.NewPromptMessage(mcp.RoleAssistant, mcp.NewTextContent(content))},
        ), nil
    }
}
```

## File Structure

```
pkg/skills/
├── engine.go          # Engine: Load, Render, Skills, ValidateMap
├── engine_test.go     # unit tests with embedded test templates
├── types.go           # SkillMeta, SkillData, ArgDef
├── discover.go        # caller skill discovery (scan CWD)
├── discover_test.go
├── graph.go           # bidirectional graph computation
└── graph_test.go

config/skills.d/
├── _map.yaml          # skill graph map (FR-12)
├── _fragments/
│   ├── evidence-table.md
│   ├── verification-gate.md
│   ├── delegation-tree.md
│   ├── priority-scoring.md
│   └── integrity-commandments.md
├── guide.md           # migration from handleGuidePrompt
├── investigate.md     # migration from handleInvestigatePrompt
├── workflow.md        # migration from handleWorkflowPrompt
├── background.md      # migration from handleBackgroundPrompt
├── review.md          # rewrite from worktree
├── debug.md           # rewrite from worktree
├── consensus.md       # rewrite from worktree
├── audit.md           # rewrite from worktree
├── agent-exec.md      # rewrite from worktree
├── research.md        # rewrite from worktree
├── security.md        # new skill
├── delegate.md        # new skill (from nvmd-ai-kit patterns)
└── tdd.md             # new skill (from ECC patterns)
```

## Phases

### Phase 1: Engine Foundation (pkg/skills/)
Build the core engine: Load, parse frontmatter, render Go templates, handle fragments.
No MCP integration yet — unit-testable in isolation.

**Deliverables:**
- `pkg/skills/types.go` — SkillMeta, SkillData, ArgDef structs
- `pkg/skills/engine.go` — Load (embedded + disk), Render, Skills
- `pkg/skills/engine_test.go` — tests with inline test templates
- One example skill template (`config/skills.d/debug.md`) to validate rendering

**Why first:** Everything else depends on the engine. Testable without server.

### Phase 2: Graph Map + Validation
Build the skill graph map and validation layer.

**Deliverables:**
- `config/skills.d/_map.yaml` — complete graph of all planned skills
- `pkg/skills/graph.go` — bidirectional graph computation from frontmatter
- `pkg/skills/graph_test.go`
- Engine.ValidateMap() — checks map ↔ frontmatter consistency
- 5 shared fragments in `_fragments/`

**Why second:** Map is prerequisite for authoring skills (FR-12). Fragments needed by skills.

### Phase 3: Skill Templates (13 skills)
Author all skill templates FROM the graph map.

**Deliverables:**
- 4 migrations: guide, investigate, workflow, background (from Go handlers)
- 6 rewrites: review, debug, consensus, audit, agent-exec, research (from worktree)
- 3 new: security, delegate, tdd
- Each 100-300 lines with phases, gates, conditionals, cross-references

**Why third:** Engine + map + fragments must exist before skills can reference them.

### Phase 4: Server Integration + Think Fix
Wire skill engine into MCP server. Fix think tool schema.

**Deliverables:**
- Auto-registration of skill-based prompts in server.go
- `handleSkillPrompt()` generic handler
- `buildSkillData()` helper collecting live data
- Migrate/remove old Go prompt handlers (keep as fallback during transition)
- Think tool: add 5 JSON-string parameters to MCP schema
- Think tool: pattern validators parse JSON from string params

**Why fourth:** Needs working engine + skills to integrate.

### Phase 5: Caller Discovery + Polish
Implement NFR-5 caller skill awareness.

**Deliverables:**
- `pkg/skills/discover.go` — scan CWD for caller skills
- `CallerHasSkill()` template function
- Conditional sections in skill templates using discovered data
- Final cleanup: remove old prompt handlers from server.go
- Update server instructions (WithInstructions)

**Why last:** Enhancement on top of working system. Not blocking.

## Library Decisions

| Component | Library | Version | Rationale |
|-----------|---------|---------|-----------|
| Template | text/template | stdlib | Conditionals, range, nested templates. No external dep. |
| YAML | gopkg.in/yaml.v3 | v3.0.1 | Already in go.mod. Frontmatter parsing. |
| Embed | embed | stdlib | Single-binary distribution. |
| All others | Custom | — | Simple enough, no library needed |

## Unknowns and Risks

| Unknown | Impact | Resolution Strategy |
|---------|--------|-------------------|
| Go template syntax conflict with MCP tool examples | HIGH | Use `{{"{{"}}` escaping or backtick fences. Test first template. |
| go:embed doesn't support `**/*.md` (only single dir) | MEDIUM | Use `//go:embed config/skills.d` for whole dir tree |
| Caller CWD might not be set on MCP connect | MEDIUM | Graceful degradation — CallerSkills = empty if no CWD |
| NFR-4 duplicate numbering in spec | LOW | Fix in spec (currently two NFR-4s) |

## Constitution Compliance

| Principle | Compliance |
|-----------|-----------|
| P1: No CLI Writes Files | ✅ Skills generate instructions, don't write files |
| P3: Correct Over Simple | ✅ Full engine with graph validation, not string builders |
| P8: Single Source of Config | ✅ Skills in config/skills.d/, not hardcoded in Go |
| P9: CLI Profiles = Plugin Dirs | ✅ Skills follow same config-as-files pattern |
| P14: Prompts = Reusable Fragments | ✅ Shared fragments with includes |
