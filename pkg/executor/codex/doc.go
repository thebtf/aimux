// Package codex implements the Codex executor for aimux.
//
// CONTRACT: All public error returns from this package MUST be *types.CLIError.
// Internal helpers may return plain error; the public boundary wraps via mapToCliError.
// Callers can use errors.As(err, &cliErr) to extract the typed code.
//
// Wire protocol: JSON-RPC 2.0 over stdio JSONL framing. The AppServerProcess
// connects to `codex app-server` and manages the lifecycle of one codex process
// per project. CodexPool maintains one process per project ID with idle eviction.
// CodexWorker is a loom.Worker that dispatches task execution through the pool.
//
// Error contract (AIMUX-18 CR-004):
//   - Worker.Execute returns *types.CLIError on failure.
//   - Pool.Acquire returns *types.CLIError on failure (BinaryNotFound for missing binary).
//   - Generic task/code/review workers surface user-visible failures through Loom
//     task results and typed CLIError metadata.
package codex
