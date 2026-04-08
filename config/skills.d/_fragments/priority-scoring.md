### Priority Scoring

**Classification:** Priority based on Severity + Impact

| Priority | Severity | Impact | Action |
|----------|----------|--------|--------|
| **P0** | Critical — data loss, security breach | All users affected | Fix immediately, block release |
| **P1** | High — feature broken, wrong results | Many users affected | Fix before next commit |
| **P2** | Medium — degraded experience, workaround exists | Some users affected | Fix in current phase |
| **P3** | Low — cosmetic, minor inconvenience | Few users notice | Fix when convenient |

**Triage rules:**
- P0 + P1: fix NOW, before any new work
- P2: fix in current iteration, do not defer
- P3: fix if touching the same file, otherwise backlog
