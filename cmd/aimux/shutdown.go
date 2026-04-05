package main

// Graceful shutdown is handled inline in main.go via context cancellation
// and deferred Close() calls. The MCP server's ServeStdio blocks until
// the transport is closed (stdin EOF or process kill).
//
// Shutdown sequence (NFR-6):
// 1. SIGTERM/SIGINT received → context cancelled
// 2. ServeStdio returns (stdin closed)
// 3. Deferred cleanup in reverse order:
//    - Logger.Close() flushes pending log entries
//    - Future: WAL.Flush() + WAL.Close()
//    - Future: Store.Close() (SQLite)
//    - Future: kill remaining child processes
//
// The 30s drain timeout for in-flight jobs will be added when
// the job execution is wired to context cancellation (Phase 8).
//
// For now, the stdio transport handles graceful shutdown via Go's
// deferred cleanup pattern — no explicit shutdown orchestration needed.
