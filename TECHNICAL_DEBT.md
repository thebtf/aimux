# Technical Debt

## Rate Limiter Not Wired Into Tool Handlers

**What:** `pkg/ratelimit/limiter.go` exists as a per-tool token bucket rate limiter but is
created in `pkg/server/server.go` (line ~153) and never consulted by any tool handler.
The `pkg/ratelimit` package is explicitly marked ARCHIVED in its package doc comment.

**Why deferred:** CLI subprocess spawning is inherently self-limiting — each spawn takes
seconds of latency and 50-200MB of memory. Token bucket rate limiting is the appropriate
protection mechanism for millisecond-latency direct API calls, not for subprocess dispatch.
The real concurrency guard is `checkConcurrencyLimit` in `server_exec.go`, which caps the
number of parallel async jobs.

**Impact:** None in current CLI-dispatch architecture. Token bucket becomes a meaningful
protection layer only if/when a direct API mode (no subprocess) is added (planned for v0.2+).

**Value/Risk:** Low value (unused code path), low risk (concurrency limit provides the
real guard). Wire up `ratelimit` when direct API mode ships.

**Tracking:** SEC-MED-1 from 2026-04-15 production readiness audit.
