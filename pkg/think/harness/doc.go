// Package harness implements the caller-centered thinking session boundary.
//
// The package does not own the final answer for a caller. It tracks visible
// task frames, knowledge artifacts, moves, evidence, gates, confidence factors,
// and stop decisions so the caller can reason with controlled process feedback.
//
// Harness traces must not persist hidden or private reasoning. Persisted data is
// limited to caller-submitted work products, evidence references, structured
// summaries, gate decisions, confidence factors, and stop reasons.
package harness
