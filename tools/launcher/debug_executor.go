// Package main — debug_executor.go implements the L1 ExecutorV2 decorator.
//
// debugExecutor wraps any types.ExecutorV2 and emits structured JSONL events
// before and after each Send/SendStream call.  The decorator is backend-agnostic:
// it works identically over CLI adapters (pipe/conpty/pty) and HTTP API executors
// (OpenAI/Anthropic/Google AI).
//
// Event sequence per Send call:
//
//	spawn_args  — resolved args / executor info, emitted before inner.Send
//	complete    — full Response (content, exit code, tokens, duration), after return
//	classify    — ErrorClass determined from response content + exit code
//	breaker_state  — optional; emitted when a BreakerRegistry is provided
//	cooldown_state — optional; emitted when a ModelCooldownTracker is provided
//
// SendStream mirrors the same sequence plus a chunk event per streaming fragment.
package main

import (
	"context"
	"fmt"
	"time"

	"github.com/thebtf/aimux/pkg/executor"
	"github.com/thebtf/aimux/pkg/types"
)

// breakerStateString converts a BreakerState int to its canonical name.
func breakerStateString(s executor.BreakerState) string {
	switch s {
	case executor.BreakerClosed:
		return "Closed"
	case executor.BreakerOpen:
		return "Open"
	case executor.BreakerHalfOpen:
		return "HalfOpen"
	default:
		return fmt.Sprintf("Unknown(%d)", int(s))
	}
}

// errorClassName converts an ErrorClass int to its canonical name.
func errorClassName(c executor.ErrorClass) string {
	switch c {
	case executor.ErrorClassNone:
		return "None"
	case executor.ErrorClassQuota:
		return "Quota"
	case executor.ErrorClassModelUnavailable:
		return "ModelUnavailable"
	case executor.ErrorClassTransient:
		return "Transient"
	case executor.ErrorClassFatal:
		return "Fatal"
	default:
		return "Unknown"
	}
}

// debugExecutor is the L1 decorator.  It satisfies types.ExecutorV2 and
// delegates every method call to the wrapped inner executor while emitting
// structured JSONL events through the sink.
type debugExecutor struct {
	inner    types.ExecutorV2
	sink     EventSink
	cliName  string // used for breaker registry lookup; empty for API executors
	breakers *executor.BreakerRegistry    // optional; nil → no breaker_state events
	cooldown types.ModelCooldownTracker   // optional; nil → no cooldown_state events
}

// newDebugExecutor wraps inner in a debugExecutor.  Provide breakers and
// cooldown when available for richer event output; both may be nil.
func newDebugExecutor(
	inner types.ExecutorV2,
	sink EventSink,
	cliName string,
	breakers *executor.BreakerRegistry,
	cooldown types.ModelCooldownTracker,
) *debugExecutor {
	return &debugExecutor{
		inner:    inner,
		sink:     sink,
		cliName:  cliName,
		breakers: breakers,
		cooldown: cooldown,
	}
}

// Info delegates to the inner executor.
func (d *debugExecutor) Info() types.ExecutorInfo {
	return d.inner.Info()
}

// IsAlive delegates to the inner executor.
func (d *debugExecutor) IsAlive() types.HealthStatus {
	return d.inner.IsAlive()
}

// Close delegates to the inner executor.
func (d *debugExecutor) Close() error {
	return d.inner.Close()
}

// emitSpawnArgs emits a spawn_args event from the message metadata and
// executor info.  For CLI executors the metadata carries command/args/cwd;
// for API executors it carries model/prompt shape info.
func (d *debugExecutor) emitSpawnArgs(msg types.Message) {
	info := d.inner.Info()

	payload := spawnArgsPayload{
		Executor: info.Name,
	}

	// Extract well-known metadata keys populated by spawnArgsToMetadata.
	if m := msg.Metadata; m != nil {
		if v, ok := m["command"].(string); ok {
			payload.Command = v
		}
		if v, ok := m["args"].([]string); ok {
			payload.Args = v
		}
		if v, ok := m["cwd"].(string); ok {
			payload.CWD = v
		}
		if v, ok := m["model"].(string); ok {
			payload.Model = v
		}
	}

	d.sink.Emit(KindSpawnArgs, payload)
}

// emitComplete emits a complete event from the response and elapsed duration.
func (d *debugExecutor) emitComplete(resp *types.Response, err error, elapsed time.Duration) {
	payload := completePayload{
		DurationMs: elapsed.Milliseconds(),
	}
	if resp != nil {
		payload.Content = resp.Content
		payload.ExitCode = resp.ExitCode
		payload.TokensUsed = resp.TokensUsed
	}
	if err != nil {
		payload.Error = err.Error()
	}
	d.sink.Emit(KindComplete, payload)
}

// emitClassify emits a classify event using ClassifyError from pkg/executor.
func (d *debugExecutor) emitClassify(resp *types.Response, err error) {
	var content, stderr string
	var exitCode int

	if resp != nil {
		content = resp.Content
		exitCode = resp.ExitCode
	}
	if err != nil {
		stderr = err.Error()
	}

	class := executor.ClassifyError(content, stderr, exitCode)
	d.sink.Emit(KindClassify, classifyPayload{
		Class:     errorClassName(class),
		ClassCode: int(class),
	})
}

// emitBreakerState emits a breaker_state event for the current CLI if a
// BreakerRegistry was provided at construction time.
func (d *debugExecutor) emitBreakerState() {
	if d.breakers == nil || d.cliName == "" {
		return
	}
	cb := d.breakers.Get(d.cliName)
	d.sink.Emit(KindBreakerState, breakerStatePayload{
		CLI:      d.cliName,
		State:    breakerStateString(cb.State()),
		Failures: cb.Failures(),
	})
}

// emitCooldownState emits a cooldown_state event if a ModelCooldownTracker was
// provided at construction time.
func (d *debugExecutor) emitCooldownState() {
	if d.cooldown == nil {
		return
	}
	entries := d.cooldown.List()
	if entries == nil {
		entries = []types.CooldownEntry{}
	}
	d.sink.Emit(KindCooldownState, cooldownStatePayload{
		Entries: entries,
		Count:   len(entries),
	})
}

// Send emits spawn_args before delegating to inner.Send, then emits complete +
// classify + optional breaker_state + optional cooldown_state after the call
// returns.  The inner error and response are returned unmodified.
func (d *debugExecutor) Send(ctx context.Context, msg types.Message) (*types.Response, error) {
	d.emitSpawnArgs(msg)

	start := time.Now()
	resp, err := d.inner.Send(ctx, msg)
	elapsed := time.Since(start)

	d.emitComplete(resp, err, elapsed)
	d.emitClassify(resp, err)
	d.emitBreakerState()
	d.emitCooldownState()

	return resp, err
}

// SendStream emits spawn_args before delegating to inner.SendStream.  Each
// streaming chunk is forwarded to onChunk and mirrored to the sink as a chunk
// event.  After the stream completes, complete + classify + optional state
// events are emitted.
func (d *debugExecutor) SendStream(ctx context.Context, msg types.Message, onChunk func(types.Chunk)) (*types.Response, error) {
	d.emitSpawnArgs(msg)

	// Determine stream discriminator from executor type.
	info := d.inner.Info()
	streamLabel := "api_delta"
	if info.Type == types.ExecutorTypeCLI {
		streamLabel = "cli_line"
	}

	wrappedChunk := func(c types.Chunk) {
		d.sink.Emit(KindChunk, chunkPayload{
			Content: c.Content,
			Done:    c.Done,
			Stream:  streamLabel,
		})
		if onChunk != nil {
			onChunk(c)
		}
	}

	start := time.Now()
	resp, err := d.inner.SendStream(ctx, msg, wrappedChunk)
	elapsed := time.Since(start)

	d.emitComplete(resp, err, elapsed)
	d.emitClassify(resp, err)
	d.emitBreakerState()
	d.emitCooldownState()

	return resp, err
}
