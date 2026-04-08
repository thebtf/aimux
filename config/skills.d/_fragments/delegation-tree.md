### Delegation Decision Tree

| Task Size | Route |
|-----------|-------|
| <5 lines, trivial | Direct edit (no delegation) |
| 5-20 lines, 1 file | exec(role="coding") sync |
| >20 lines or multi-file | exec(role="coding", async=true) |
| TDD cycle or 2+ failures | exec(role="coding", async=true) with test-first |
| 1M context / broad scan | exec(role="analyze") — long-context model |
| Parallel subtask | exec(role="coding", async=true) × N |
| Pure reasoning, no I/O | think(pattern=...) |

### QUICK Delegation Format

```
TASK: [one sentence — what to implement]
CONTEXT: [files to read/modify, current state, stack]
CONSTRAINTS: [patterns to follow, what must not change]
MUST NOT: [fake backends, claim done without demo]
DONE WHEN: [verifiable outcome 1, verifiable outcome 2]
```
