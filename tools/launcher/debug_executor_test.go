// Package main — debug_executor_test.go: unit tests for the L1 ExecutorV2 decorator.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/thebtf/aimux/pkg/executor"
	"github.com/thebtf/aimux/pkg/types"
)

// fakeExecutor is a minimal types.ExecutorV2 for unit tests.
type fakeExecutor struct {
	info        types.ExecutorInfo
	sendResp    *types.Response
	sendErr     error
	streamChunks []types.Chunk
	streamErr   error
	closeErr    error
	closeCalled bool
}

func (f *fakeExecutor) Info() types.ExecutorInfo { return f.info }

func (f *fakeExecutor) IsAlive() types.HealthStatus { return types.HealthAlive }

func (f *fakeExecutor) Close() error {
	f.closeCalled = true
	return f.closeErr
}

func (f *fakeExecutor) Send(_ context.Context, _ types.Message) (*types.Response, error) {
	return f.sendResp, f.sendErr
}

func (f *fakeExecutor) SendStream(_ context.Context, _ types.Message, onChunk func(types.Chunk)) (*types.Response, error) {
	for _, c := range f.streamChunks {
		if onChunk != nil {
			onChunk(c)
		}
	}
	return f.sendResp, f.streamErr
}

// captureSink records all Emit calls in order for assertion.
type captureSink struct {
	events []capturedEvent
}

type capturedEvent struct {
	Kind    string
	Payload json.RawMessage
}

func (c *captureSink) Emit(kind string, payload any) {
	raw, _ := json.Marshal(payload)
	c.events = append(c.events, capturedEvent{Kind: kind, Payload: raw})
}

func (c *captureSink) kindsOf() []string {
	out := make([]string, len(c.events))
	for i, e := range c.events {
		out[i] = e.Kind
	}
	return out
}

func newTestBreakers() *executor.BreakerRegistry {
	return executor.NewBreakerRegistry(executor.BreakerConfig{FailureThreshold: 3, CooldownSeconds: 60, HalfOpenMaxCalls: 1})
}

func newTestCooldown() types.ModelCooldownTracker { return executor.NewModelCooldownTracker() }

func makeTestMessage() types.Message {
	return types.Message{Content: "test prompt", Metadata: map[string]any{"command": "codex", "executor": "pipe"}}
}

// TestDebugExecutor_Send_EmitsExpectedEvents: successful Send emits exactly
// spawn_args, complete, classify, breaker_state, cooldown_state (5 events).
func TestDebugExecutor_Send_EmitsExpectedEvents(t *testing.T) {
	t.Parallel()

	fake := &fakeExecutor{
		info: types.ExecutorInfo{Name: "pipe", Type: types.ExecutorTypeCLI},
		sendResp: &types.Response{
			Content:  "hello from codex",
			ExitCode: 0,
		},
	}

	sink := &captureSink{}
	dbg := newDebugExecutor(fake, sink, "codex", newTestBreakers(), newTestCooldown())

	resp, err := dbg.Send(context.Background(), makeTestMessage())
	if err != nil {
		t.Fatalf("unexpected Send error: %v", err)
	}
	if resp == nil || resp.Content != "hello from codex" {
		t.Fatalf("unexpected response: %+v", resp)
	}

	kinds := sink.kindsOf()
	want := []string{KindSpawnArgs, KindComplete, KindClassify, KindBreakerState, KindCooldownState}
	if len(kinds) != len(want) {
		t.Fatalf("events %v, want %v", kinds, want)
	}
	for i, k := range want {
		if kinds[i] != k {
			t.Errorf("event[%d]: want %q got %q", i, k, kinds[i])
		}
	}

	var cp completePayload
	if err := json.Unmarshal(sink.events[1].Payload, &cp); err != nil {
		t.Fatalf("unmarshal complete: %v", err)
	}
	if cp.Content != "hello from codex" {
		t.Errorf("complete.content = %q, want %q", cp.Content, "hello from codex")
	}
	if cp.ExitCode != 0 {
		t.Errorf("complete.exit_code = %d, want 0", cp.ExitCode)
	}

	var clp classifyPayload
	if err := json.Unmarshal(sink.events[2].Payload, &clp); err != nil {
		t.Fatalf("unmarshal classify: %v", err)
	}
	if clp.Class != "None" {
		t.Errorf("classify.class = %q, want %q", clp.Class, "None")
	}
}

// TestDebugExecutor_Send_ErrorPath: classify event reflects the correct ErrorClass.
func TestDebugExecutor_Send_ErrorPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		resp      *types.Response
		err       error
		wantClass string
	}{
		{
			name:      "quota error",
			resp:      &types.Response{Content: "429 rate limit exceeded", ExitCode: 1},
			err:       nil,
			wantClass: "Quota",
		},
		{
			name:      "fatal auth error",
			resp:      &types.Response{Content: "unauthorized", ExitCode: 1},
			err:       nil,
			wantClass: "Fatal",
		},
		{
			name:      "unknown non-zero exit",
			resp:      &types.Response{Content: "something went wrong", ExitCode: 1},
			err:       nil,
			wantClass: "Unknown",
		},
		{
			name:      "error with nil resp",
			resp:      nil,
			err:       errors.New("rate limit hit your usage limit"),
			wantClass: "Quota",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fake := &fakeExecutor{
				info:     types.ExecutorInfo{Name: "pipe", Type: types.ExecutorTypeCLI},
				sendResp: tc.resp,
				sendErr:  tc.err,
			}

			sink := &captureSink{}
			dbg := newDebugExecutor(fake, sink, "codex", newTestBreakers(), newTestCooldown())
			_, _ = dbg.Send(context.Background(), makeTestMessage())

			var clp classifyPayload
			for _, ev := range sink.events {
				if ev.Kind == KindClassify {
					_ = json.Unmarshal(ev.Payload, &clp)
					break
				}
			}
			if clp.Class != tc.wantClass {
				t.Errorf("classify.class = %q, want %q", clp.Class, tc.wantClass)
			}
		})
	}
}

// TestDebugExecutor_SendStream_EmitsChunks: SendStream emits spawn_args, N chunk
// events, then complete + classify + breaker_state + cooldown_state.
func TestDebugExecutor_SendStream_EmitsChunks(t *testing.T) {
	t.Parallel()

	chunks := []types.Chunk{
		{Content: "part1", Done: false},
		{Content: "part2", Done: false},
		{Content: "", Done: true},
	}

	fake := &fakeExecutor{
		info:         types.ExecutorInfo{Name: "pipe", Type: types.ExecutorTypeCLI},
		streamChunks: chunks,
		sendResp:     &types.Response{Content: "part1part2", ExitCode: 0},
	}

	sink := &captureSink{}
	dbg := newDebugExecutor(fake, sink, "codex", newTestBreakers(), newTestCooldown())

	var received []types.Chunk
	resp, err := dbg.SendStream(context.Background(), makeTestMessage(), func(c types.Chunk) {
		received = append(received, c)
	})
	if err != nil {
		t.Fatalf("unexpected SendStream error: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response")
	}

	if len(received) != len(chunks) {
		t.Fatalf("onChunk called %d times, want %d", len(received), len(chunks))
	}

	kinds := sink.kindsOf()
	// spawn_args + N chunks + complete + classify + breaker_state + cooldown_state
	wantTotal := 1 + len(chunks) + 4
	if len(kinds) != wantTotal {
		t.Fatalf("expected %d events, got %d: %v", wantTotal, len(kinds), kinds)
	}
	if kinds[0] != KindSpawnArgs {
		t.Errorf("event[0] = %q, want %q", kinds[0], KindSpawnArgs)
	}
	for i := 1; i <= len(chunks); i++ {
		if kinds[i] != KindChunk {
			t.Errorf("event[%d] = %q, want %q", i, kinds[i], KindChunk)
		}
	}

	var lastChunk chunkPayload
	if err := json.Unmarshal(sink.events[len(chunks)].Payload, &lastChunk); err != nil {
		t.Fatalf("unmarshal last chunk: %v", err)
	}
	if !lastChunk.Done {
		t.Error("last chunk event Done != true")
	}

	tail := kinds[1+len(chunks):]
	wantTail := []string{KindComplete, KindClassify, KindBreakerState, KindCooldownState}
	for i, k := range wantTail {
		if tail[i] != k {
			t.Errorf("tail event[%d] = %q, want %q", i, tail[i], k)
		}
	}
}

// TestDebugExecutor_PassThroughInfoIsAliveClose: Info/IsAlive/Close delegate to inner.
func TestDebugExecutor_PassThroughInfoIsAliveClose(t *testing.T) {
	t.Parallel()

	fake := &fakeExecutor{
		info: types.ExecutorInfo{Name: "pipe", Type: types.ExecutorTypeCLI},
	}

	sink := &captureSink{}
	dbg := newDebugExecutor(fake, sink, "codex", nil, nil)

	info := dbg.Info()
	if info.Name != "pipe" {
		t.Errorf("Info().Name = %q, want %q", info.Name, "pipe")
	}
	if info.Type != types.ExecutorTypeCLI {
		t.Errorf("Info().Type = %v, want %v", info.Type, types.ExecutorTypeCLI)
	}
	if h := dbg.IsAlive(); h != types.HealthAlive {
		t.Errorf("IsAlive() = %v, want HealthAlive", h)
	}
	if err := dbg.Close(); err != nil {
		t.Fatalf("Close() error: %v", err)
	}
	if !fake.closeCalled {
		t.Error("inner.Close was not called")
	}
	if len(sink.events) != 0 {
		t.Errorf("expected no events from Info/IsAlive/Close, got %d: %v", len(sink.events), sink.kindsOf())
	}
}
