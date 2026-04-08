### Verification Gate

**Consensus ≠ Correctness:** Three models agreeing doesn't mean they're right.
Provider outputs can be hallucinated. Synthesis may be stale — check timestamps.

**Evidence Classification:**
- **VERIFIED** — confirmed this session via tool output
- **INFERRED** — reasonable conclusion from verified facts, not directly confirmed
- **STALE** — from training memory or model output, not verified this session
- **BLOCKED** — cannot verify (tool unavailable, source unreachable)
- **UNKNOWN** — no basis at all, pure guess → STOP and look up
