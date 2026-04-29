package main

// handoff_compat.go — ModeDirect removal compatibility note (v5.1)
//
// ADR-003: muxcore upstream reference cleanup hook.
//
// In the former ModeDirect path, muxcore daemon maintained an "upstream ref" —
// a live reference to the child process spawned via engine.Config.Command.
// During graceful-restart handoff, the new daemon needed to release these refs
// before the old daemon could exit cleanly.
//
// Since v5 (engine.Config.SessionHandler path), the daemon operates in
// SessionHandler-only mode: engine.Config.Command is set on the SHIM side only
// (for daemon cold-start self-spawn), not on the daemon side. The daemon-side
// engine.Config contains no Command field — only Name, Persistent, SessionHandler,
// and Logger.
//
// Consequence: muxcore v0.22.0 does NOT emit upstream refs in the daemon's
// handoff snapshot when running in SessionHandler-only mode. There are no child
// processes to detach. The handoff cleanup hook is therefore a no-op and requires
// no implementation.
//
// If a future muxcore version reintroduces upstream refs under SessionHandler mode,
// add a hook here that calls d.HandleReleaseUpstreams() (or equivalent) before
// the old daemon exits. Cross-reference: ADR-003 in
// .agent/specs/drop-mode-direct-legacy/architecture.md.
