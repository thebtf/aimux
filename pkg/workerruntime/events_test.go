package workerruntime

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

const (
	testPrivateReasoningSentinel = "PRIVATE_REASONING_SENTINEL"
	testSecretValue              = "sk-proj-abcdefghijklmnopqrstuvwxyz0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ"
)

type testObservedWrite struct {
	channel string
	data    []byte
}

type testEventProjection struct {
	Channel, Type, Payload string
	Redacted, Truncated    bool
	Terminal               bool
}

func TestEventIngest_FragmentationMatchesUnsplitSemanticProjection(t *testing.T) {
	writes := testMixedWrites()
	baseline := testRunWrites(t, writes)
	testAssertMixedEvents(t, baseline)
	want := testProjectEvents(t, baseline)

	for split := 1; split < testWriteBytes(writes); split++ {
		t.Run(fmt.Sprintf("split_%03d", split), func(t *testing.T) {
			got := testRunWrites(t, testSplitWrites(writes, split))
			testAssertMixedEvents(t, got)
			testEqualProjection(t, want, testProjectEvents(t, got))
		})
	}

	plans := []struct {
		name string
		run  func([]testObservedWrite) []testObservedWrite
	}{
		{"one_byte", func(in []testObservedWrite) []testObservedWrite { return testChunkWrites(in, func() int { return 1 }) }},
		{"prime_cycle", func(in []testObservedWrite) []testObservedWrite {
			primes, index := []int{2, 3, 5, 7, 11}, 0
			return testChunkWrites(in, func() int { n := primes[index%len(primes)]; index++; return n })
		}},
		{"fixed_random", func(in []testObservedWrite) []testObservedWrite {
			state := uint32(0x00c0ffee)
			return testChunkWrites(in, func() int { state = state*1664525 + 1013904223; return int(state%13) + 1 })
		}},
	}
	for _, plan := range plans {
		t.Run(plan.name, func(t *testing.T) {
			got := testRunWrites(t, plan.run(writes))
			testAssertMixedEvents(t, got)
			testEqualProjection(t, want, testProjectEvents(t, got))
		})
	}
}

func TestProviderJSONL_FramesAreAtomicAcrossFragmentation(t *testing.T) {
	tests := []struct {
		name     string
		config   eventNormalizerConfig
		frame    []byte
		wantText string
	}{
		{
			"codex",
			eventNormalizerConfig{Provider: "codex", Format: "jsonl", SchemaFingerprint: "codex-app-server-v1", MaxFrameBytes: 4096, MaxPayloadBytes: 512},
			[]byte("{\"type\":\"item.completed\",\"item\":{\"type\":\"agent_message\",\"text\":\"Codex: Привет 👋\"}}"),
			"Codex: Привет 👋",
		},
		{
			"grok",
			eventNormalizerConfig{Provider: "grok", Format: "jsonl", SchemaFingerprint: "grok-acp-v1", MaxFrameBytes: 4096, MaxPayloadBytes: 512},
			[]byte("{\"method\":\"session/update\",\"params\":{\"update\":{\"sessionUpdate\":\"agent_message_chunk\",\"content\":\"Grok: Здравствуй 🤖\"}}}"),
			"Grok: Здравствуй 🤖",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			baselineNormalizer := testNewNormalizer(t, tt.config)
			baseline := testFeed(t, baselineNormalizer, "process.stdout", append(append([]byte(nil), tt.frame...), '\n'))
			if len(baseline) != 1 || baseline[0].Channel != "assistant" || baseline[0].Type != "text_delta" ||
				testEventText(t, baseline[0]) != tt.wantText || !utf8.ValidString(testEventText(t, baseline[0])) {
				t.Fatalf("baseline event = %#v", baseline)
			}
			want := testProjectEvents(t, baseline)

			for split := 1; split < len(tt.frame); split++ {
				n := testNewNormalizer(t, tt.config)
				if premature := testFeed(t, n, "process.stdout", tt.frame[:split]); len(premature) != 0 {
					t.Fatalf("split %d emitted before complete frame: %#v", split, premature)
				}
				suffix := append(append([]byte(nil), tt.frame[split:]...), '\n')
				testEqualProjection(t, want, testProjectEvents(t, testFeed(t, n, "process.stdout", suffix)))
			}

			n := testNewNormalizer(t, tt.config)
			for i, b := range tt.frame {
				if premature := testFeed(t, n, "process.stdout", []byte{b}); len(premature) != 0 {
					t.Fatalf("byte %d emitted before complete frame: %#v", i, premature)
				}
			}
			testEqualProjection(t, want, testProjectEvents(t, testFeed(t, n, "process.stdout", []byte{'\n'})))

			n = testNewNormalizer(t, tt.config)
			for i, b := range tt.frame {
				if premature := testFeed(t, n, "process.stdout", []byte{b}); len(premature) != 0 {
					t.Fatalf("final byte %d emitted before flush: %#v", i, premature)
				}
			}
			testEqualProjection(t, want, testProjectEvents(t, testFlush(t, n, "process.stdout")))

			nextText := tt.wantText + " / next"
			nextFrame := []byte(strings.Replace(string(tt.frame), tt.wantText, nextText, 1))
			nextNormalizer := testNewNormalizer(t, tt.config)
			nextBaseline := testFeed(t, nextNormalizer, "process.stdout", append(append([]byte(nil), nextFrame...), '\n'))
			if len(nextBaseline) != 1 || testEventText(t, nextBaseline[0]) != nextText {
				t.Fatalf("next baseline = %#v", nextBaseline)
			}
			nextWant := testProjectEvents(t, nextBaseline)

			n = testNewNormalizer(t, tt.config)
			combined := append(append(append([]byte(nil), tt.frame...), '\n'), nextFrame...)
			combined = append(combined, '\n')
			wantBoth := append(append([]testEventProjection(nil), want...), nextWant...)
			testEqualProjection(t, wantBoth, testProjectEvents(t, testFeed(t, n, "process.stdout", combined)))

			n = testNewNormalizer(t, tt.config)
			split := len(nextFrame) / 2
			firstRead := append(append(append([]byte(nil), tt.frame...), '\n'), nextFrame[:split]...)
			testEqualProjection(t, want, testProjectEvents(t, testFeed(t, n, "process.stdout", firstRead)))
			secondRead := append(append([]byte(nil), nextFrame[split:]...), '\n')
			testEqualProjection(t, nextWant, testProjectEvents(t, testFeed(t, n, "process.stdout", secondRead)))
		})
	}
}

func TestEventIngest_BuffersInterleavedChannelsIndependently(t *testing.T) {
	n := testNewNormalizer(t, eventNormalizerConfig{Provider: "generic", Format: "text", SchemaFingerprint: "generic-text-v1", MaxFrameBytes: 1024, MaxPayloadBytes: 512})
	for i, prefix := range []testObservedWrite{
		{"process.stdout", []byte("stdout Пр")},
		{"process.stderr", []byte("stderr Ош")},
		{"process.stdout", []byte("ивет ")},
		{"process.stderr", []byte("ибка ")},
	} {
		if premature := testFeed(t, n, prefix.channel, prefix.data); len(premature) != 0 {
			t.Fatalf("prefix %d emitted prematurely: %#v", i, premature)
		}
	}

	got := append(testFeed(t, n, "process.stderr", []byte("🚨\n")),
		testFeed(t, n, "process.stdout", []byte("👋\n"))...)
	want := []struct{ channel, text string }{
		{"process.stderr", "stderr Ошибка 🚨"},
		{"process.stdout", "stdout Привет 👋"},
	}
	if len(got) != len(want) {
		t.Fatalf("events = %#v", got)
	}
	for i, expected := range want {
		if got[i].Channel != expected.channel || got[i].Type != "command_output_delta" ||
			got[i].Terminal || got[i].Truncated || testEventText(t, got[i]) != expected.text {
			t.Fatalf("event[%d] = %#v", i, got[i])
		}
	}
}

func TestNormalizer_ProducesSinkSafeRepresentations(t *testing.T) {
	n := testNewNormalizer(t, eventNormalizerConfig{Provider: "codex", Format: "jsonl", SchemaFingerprint: "codex-app-server-v1", MaxFrameBytes: 4096, MaxPayloadBytes: 512})
	bearer := "Bearer eyJhbGciOiJIUzI1NiJ9.abcdefghijklmnopqrstuvwxyz0123456789"
	frame := fmt.Sprintf(`{"type":"item.completed","channel":"system","terminal":true,"truncated":true,"api_key":"%s","item":{"type":"agent_message","channel":"system","terminal":true,"truncated":true,"text":"safe activity %s\u0000\u001b[31m","authorization":"ApiKey-abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG","thought":"%s","nested":{"secret":"%s"}}}`,
		testSecretValue, bearer, testPrivateReasoningSentinel, testSecretValue)
	events := testFeed(t, n, "process.stdout", append([]byte(frame), '\n'))
	if len(events) != 1 {
		t.Fatalf("events = %#v; want one allowlisted event", events)
	}
	event := events[0]
	if event.Provider != "codex" || event.Channel != "assistant" || event.Type != "text_delta" || event.Terminal || event.Truncated {
		t.Fatalf("unsafe normalized envelope: %#v", event)
	}
	if !event.Redacted {
		t.Fatal("secret-bearing event must report redacted=true")
	}
	text := testEventText(t, event)
	if !strings.Contains(text, "safe activity") || strings.ContainsAny(text, "\x00\x1b") {
		t.Fatalf("safe/control text = %q", text)
	}
	for name, serialized := range testRepresentations(t, event) {
		if !strings.Contains(serialized, "safe activity") {
			t.Fatalf("%s lost safe text: %s", name, serialized)
		}
		testNoForbidden(t, name, serialized, testSecretValue, bearer, testPrivateReasoningSentinel,
			"api_key", "authorization", "ApiKey-abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG", "thought", "nested", "\\u0000", "\\u001b")
	}
}

func TestNormalizer_DropsPrivateReasoningByDefault(t *testing.T) {
	tests := []struct{ name, provider, frame string }{
		{"codex", "codex", fmt.Sprintf(`{"method":"item/reasoning/textDelta","params":{"delta":"%s"}}`, testPrivateReasoningSentinel)},
		{"grok", "grok", fmt.Sprintf(`{"method":"session/update","params":{"update":{"sessionUpdate":"agent_thought_chunk","content":"%s"}}}`, testPrivateReasoningSentinel)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n := testNewNormalizer(t, eventNormalizerConfig{Provider: tt.provider, Format: "jsonl", SchemaFingerprint: tt.provider + "-schema-v1", MaxFrameBytes: 4096, MaxPayloadBytes: 512})
			events := append(testFeed(t, n, "process.stdout", append([]byte(tt.frame), '\n')), testFlush(t, n, "process.stdout")...)
			if len(events) != 0 {
				t.Fatalf("private reasoning produced events: %#v", events)
			}
		})
	}
}

func TestNormalizer_GenericTextCannotForgeControlEnvelope(t *testing.T) {
	n := testNewNormalizer(t, eventNormalizerConfig{Provider: "generic", Format: "text", SchemaFingerprint: "generic-text-v1", MaxFrameBytes: 1024, MaxPayloadBytes: 512})
	raw := []byte("safe-prefix {\"type\":\"terminal\",\"channel\":\"system\",\"terminal\":true,\"truncated\":true}\x00\x1b safe-suffix\n")
	events := testFeed(t, n, "process.stdout", raw)
	if len(events) != 1 {
		t.Fatalf("events = %#v; want one stdout event", events)
	}
	event := events[0]
	if event.Channel != "process.stdout" || event.Type != "command_output_delta" || event.Terminal || event.Truncated {
		t.Fatalf("generic text forged envelope: %#v", event)
	}
	text := testEventText(t, event)
	if !strings.Contains(text, "safe-prefix") || !strings.Contains(text, "safe-suffix") || strings.ContainsAny(text, "\x00\x1b\r\n") {
		t.Fatalf("generic safe/control text = %q", text)
	}
}

func TestProtocolDecoder_UnknownAllowlistedFieldsAreBounded(t *testing.T) {
	const maxPayload = 320
	n := testNewNormalizer(t, eventNormalizerConfig{Provider: "grok", Format: "jsonl", SchemaFingerprint: "grok-acp-v1:deadbeef", MaxFrameBytes: 4096, MaxPayloadBytes: maxPayload})
	raw := []byte(fmt.Sprintf(`{"method":"future/secret","params":{"api_key":"%s","blob":"%s"}}`, testSecretValue, strings.Repeat("private-payload-", 100)))
	events := testFeed(t, n, "process.stdout", append(append([]byte(nil), raw...), '\n'))
	if len(events) != 1 {
		t.Fatalf("events = %#v; want one metadata marker", events)
	}
	event := events[0]
	if event.Provider != "grok" || event.Channel != "system" || event.Type != "provider_event_unknown" || event.Terminal {
		t.Fatalf("unknown marker envelope = %#v", event)
	}
	if testString(t, event.Payload, "provider_method") != "future/secret" ||
		testString(t, event.Payload, "schema_fingerprint") != "grok-acp-v1:deadbeef" ||
		testUint(t, event.Payload, "byte_length") != uint64(len(raw)) {
		t.Fatalf("unknown marker metadata = %#v", event.Payload)
	}
	hash := testString(t, event.Payload, "diagnostic_hash")
	if hash == "" || len(hash) > 128 || len(testJSON(t, event.Payload)) > maxPayload {
		t.Fatalf("unknown marker is unbounded: %#v", event.Payload)
	}
	testNoForbidden(t, "unknown", string(testJSON(t, event)), testSecretValue, "private-payload-", "api_key", "blob", "params")
}

func TestEventNormalizer_EnforcesFrameAndPayloadLimits(t *testing.T) {
	const frameLimit = 17
	for _, size := range []int{frameLimit - 1, frameLimit, frameLimit + 1} {
		t.Run(fmt.Sprintf("frame_%d", size), func(t *testing.T) {
			n := testNewNormalizer(t, eventNormalizerConfig{Provider: "generic", Format: "text", SchemaFingerprint: "generic-text-v1", MaxFrameBytes: frameLimit, MaxPayloadBytes: 128})
			text := strings.Repeat("x", size)
			if size == frameLimit+1 {
				text = "oversize-frame-raw"
			}
			events := append(testFeed(t, n, "process.stderr", append([]byte(text), '\n')), testFlush(t, n, "process.stderr")...)
			if len(events) != 1 {
				t.Fatalf("events = %#v; want one boundary event", events)
			}
			if size <= frameLimit {
				if events[0].Type != "command_output_delta" || events[0].Truncated || testEventText(t, events[0]) != text {
					t.Fatalf("in-limit frame = %#v", events[0])
				}
				return
			}
			if events[0].Type != "output_truncated" || !events[0].Truncated || events[0].Terminal ||
				testUint(t, events[0].Payload, "dropped_events") != 1 || testUint(t, events[0].Payload, "dropped_bytes") != uint64(size) ||
				strings.Contains(string(testJSON(t, events[0])), text) {
				t.Fatalf("oversize frame marker = %#v", events[0])
			}
		})
	}

	t.Run("payload", func(t *testing.T) {
		const limit = 64
		n := testNewNormalizer(t, eventNormalizerConfig{Provider: "codex", Format: "jsonl", SchemaFingerprint: "codex-app-server-v1", MaxFrameBytes: 1024, MaxPayloadBytes: limit})
		frame := map[string]any{"type": "item.completed", "item": map[string]any{"type": "agent_message", "text": strings.Repeat("界", 100)}}
		events := testFeed(t, n, "process.stdout", append(testJSON(t, frame), '\n'))
		if len(events) != 1 || !events[0].Truncated || events[0].Terminal || len(testJSON(t, events[0].Payload)) > limit || !utf8.ValidString(testEventText(t, events[0])) {
			t.Fatalf("payload limit event = %#v", events)
		}
	})
}

func TestEventPumpConfig_DefaultsAndHardCeilings(t *testing.T) {
	got := defaultEventPumpConfig()
	if got.MaxEvents != 4096 || got.MaxBytes != 8<<20 || got.ControlReserveEvents != 128 || got.ControlReserveBytes != 1<<20 {
		t.Fatalf("default config = %#v", got)
	}
	ceiling := eventPumpConfig{MaxEvents: 65_536, MaxBytes: 64 << 20, ControlReserveEvents: 128, ControlReserveBytes: 1 << 20}
	pump, err := newEventPump(ceiling)
	if err != nil {
		t.Fatalf("hard ceiling rejected: %v", err)
	}
	pump.close()
	for _, invalid := range []eventPumpConfig{
		{MaxEvents: 65_537, MaxBytes: ceiling.MaxBytes, ControlReserveEvents: 128, ControlReserveBytes: 1 << 20},
		{MaxEvents: ceiling.MaxEvents, MaxBytes: (64 << 20) + 1, ControlReserveEvents: 128, ControlReserveBytes: 1 << 20},
	} {
		if extra, err := newEventPump(invalid); err == nil {
			extra.close()
			t.Fatalf("above-ceiling config accepted: %#v", invalid)
		}
	}
}

func TestEventQueue_ReportsCoalescedAdmissionWithoutReordering(t *testing.T) {
	pump := testNewPump(t, eventPumpConfig{MaxEvents: 2, MaxBytes: 1024, ControlReserveEvents: 1, ControlReserveBytes: 256})
	first, second := pump.admit(testDelta("process.stdout", "a")), pump.admit(testDelta("process.stdout", "b"))
	testStatus(t, first, "admitted")
	testStatus(t, second, "coalesced")
	if first.IngestOrdinal != 1 || second.IngestOrdinal <= first.IngestOrdinal || second.CoalescedEvents != 1 || second.CoalescedBytes != 1 {
		t.Fatalf("coalescing results = %#v / %#v", first, second)
	}
	var text strings.Builder
	var ordinal uint64
	for _, event := range pump.drain(4) {
		if event.IngestOrdinal <= ordinal || event.Type == "output_truncated" {
			t.Fatalf("coalesced order/event = %#v", event)
		}
		ordinal = event.IngestOrdinal
		text.WriteString(testEventText(t, event))
	}
	if text.String() != "ab" {
		t.Fatalf("coalesced text = %q", text.String())
	}
}

func TestEventQueue_SameChannelCoalescingStopsAtOrdinaryByteCapacity(t *testing.T) {
	const ordinaryCapacity = 6
	config := eventPumpConfig{MaxEvents: 4, MaxBytes: 10, ControlReserveEvents: 1, ControlReserveBytes: 4}
	if got := config.MaxBytes - config.ControlReserveBytes; got != ordinaryCapacity {
		t.Fatalf("ordinary capacity = %d; want %d", got, ordinaryCapacity)
	}
	pump := testNewPump(t, config)
	testStatus(t, pump.admit(testDelta("process.stdout", "ab")), "admitted")
	coalesced := pump.admit(testDelta("process.stdout", "cdef"))
	testStatus(t, coalesced, "coalesced")
	if coalesced.CoalescedEvents != 1 || coalesced.CoalescedBytes != 4 {
		t.Fatalf("coalesced admission = %#v", coalesced)
	}

	overflow := testAdmitPromptly(t, pump, "same_channel_plus_one_byte", testDelta("process.stdout", "g"))
	testStatus(t, overflow, "rejected_quota")
	if overflow.IngestOrdinal != 0 {
		t.Fatalf("overflow ordinal = %d", overflow.IngestOrdinal)
	}

	events := pump.drain(4)
	if len(events) != 2 || events[0].Type != "command_output_delta" || events[1].Type != "output_truncated" {
		t.Fatalf("events = %#v", events)
	}
	if got := testEventText(t, events[0]); got != "abcdef" || events[0].Truncated {
		t.Fatalf("bounded coalesced payload = %q event=%#v", got, events[0])
	}
	gap := events[1]
	if !gap.Truncated || gap.Terminal || testUint(t, gap.Payload, "dropped_events") != 1 || testUint(t, gap.Payload, "dropped_bytes") != 1 {
		t.Fatalf("gap marker = %#v", gap)
	}
}

func TestEventQueue_ExactOrdinaryAndControlEventByteBoundaries(t *testing.T) {
	tests := []struct {
		name        string
		config      eventPumpConfig
		fill        []RuntimeEvent
		reserved    []RuntimeEvent
		overflow    RuntimeEvent
		wantTypes   []string
		wantDropped uint64
	}{
		{"ordinary_events", eventPumpConfig{MaxEvents: 5, MaxBytes: 1 << 20, ControlReserveEvents: 2, ControlReserveBytes: 1024}, testOrdinary(3), nil, testOrdinaryEvent(3), []string{"command_output_delta", "command_output_delta", "text_delta", "output_truncated"}, 1},
		{"reserve_events", eventPumpConfig{MaxEvents: 5, MaxBytes: 1 << 20, ControlReserveEvents: 2, ControlReserveBytes: 1024}, testOrdinary(3), []RuntimeEvent{testControl("status", false, "cancel_ack"), testControl("terminal", true, "completed")}, testControl("error", false, "late"), []string{"command_output_delta", "command_output_delta", "text_delta", "status", "terminal"}, 0},
		{"ordinary_bytes", eventPumpConfig{MaxEvents: 64, MaxBytes: 10, ControlReserveEvents: 8, ControlReserveBytes: 4}, []RuntimeEvent{testDelta("process.stdout", "oooooo")}, nil, testDelta("process.stderr", "x"), []string{"command_output_delta", "output_truncated"}, 1},
		{"reserve_bytes", eventPumpConfig{MaxEvents: 64, MaxBytes: 10, ControlReserveEvents: 8, ControlReserveBytes: 4}, []RuntimeEvent{testDelta("process.stdout", "oooooo")}, []RuntimeEvent{testSizedControl("error", false, "cccc")}, testSizedControl("terminal", true, "x"), []string{"command_output_delta", "error"}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pump := testNewPump(t, tt.config)
			for _, event := range tt.fill {
				testStatus(t, pump.admit(event), "admitted")
			}
			for i, event := range tt.reserved {
				testStatus(t, testAdmitPromptly(t, pump, fmt.Sprintf("reserved_%d", i), event), "admitted")
			}
			testStatus(t, testAdmitPromptly(t, pump, "plus_one", tt.overflow), "rejected_quota")
			events := pump.drain(tt.config.MaxEvents + 1)
			if len(events) != len(tt.wantTypes) {
				t.Fatalf("events = %#v; want types %v", events, tt.wantTypes)
			}
			for i, want := range tt.wantTypes {
				if events[i].Type != want {
					t.Fatalf("event[%d].Type = %q; want %q", i, events[i].Type, want)
				}
			}
			if tt.wantDropped > 0 {
				gap := events[len(events)-1]
				if !gap.Truncated || gap.Terminal || testUint(t, gap.Payload, "dropped_events") != 1 || testUint(t, gap.Payload, "dropped_bytes") != tt.wantDropped {
					t.Fatalf("gap marker = %#v", gap)
				}
			}
		})
	}
}

func TestEventQueue_BoundsMemoryAndEmitsExactGapReserveAndClosedResults(t *testing.T) {
	config := eventPumpConfig{MaxEvents: 3, MaxBytes: 4096, ControlReserveEvents: 2, ControlReserveBytes: 3072}
	pump, err := newEventPump(config)
	if err != nil {
		t.Fatalf("newEventPump: %v", err)
	}
	first := pump.admit(testDelta("process.stdout", "accepted"))
	testStatus(t, first, "admitted")
	if first.IngestOrdinal != 1 {
		t.Fatalf("first ordinal = %d", first.IngestOrdinal)
	}

	const dropped = 100
	text := strings.Repeat("d", 2048)
	start, result := make(chan struct{}), make(chan string, 1)
	go func() {
		<-start
		for i := 0; i < dropped; i++ {
			admission := pump.admit(testDelta("process.stdout", text))
			if testAdmissionStatus(admission) != "rejected_quota" || admission.IngestOrdinal != 0 {
				result <- fmt.Sprintf("admission[%d]=%#v", i, admission)
				return
			}
		}
		result <- ""
	}()
	close(start)
	select {
	case failure := <-result:
		if failure != "" {
			t.Fatal(failure)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("saturated admission blocked")
	}
	testStatus(t, pump.admit(testControl("terminal", true, "completed")), "admitted")

	events := pump.drain(4)
	if len(events) != 3 {
		t.Fatalf("events = %#v", events)
	}
	for i, want := range []string{"command_output_delta", "output_truncated", "terminal"} {
		if events[i].Type != want || events[i].IngestOrdinal != uint64(i+1) {
			t.Fatalf("event[%d] = %#v", i, events[i])
		}
	}
	gap := events[1]
	if testUint(t, gap.Payload, "dropped_events") != dropped || testUint(t, gap.Payload, "dropped_bytes") != dropped*uint64(len(text)) || !gap.Truncated || gap.Terminal {
		t.Fatalf("gap = %#v", gap)
	}
	if len(pump.drain(4)) != 0 {
		t.Fatal("queue retained hidden events")
	}
	pump.close()
	closed := pump.admit(testDelta("process.stdout", "late"))
	testStatus(t, closed, "rejected_closed")
	if closed.IngestOrdinal != 0 {
		t.Fatalf("closed ordinal = %d", closed.IngestOrdinal)
	}
}

func testMixedWrites() []testObservedWrite {
	invalid := append(append([]byte("bad"), 0xff), []byte(" SAFE_SUFFIX\n")...)
	return []testObservedWrite{
		{"process.stdout", []byte("Привет 👋\r\nstdout-lone-cr\rstdout-lone-lf\n")},
		{"process.stderr", []byte("stderr-one\n")},
		{"process.stdout", []byte("interleaved-out\n")},
		{"process.stderr", invalid},
		{"process.stdout", []byte("финал 🚀")},
	}
}

func testRunWrites(t *testing.T, writes []testObservedWrite) []RuntimeEvent {
	t.Helper()
	n := testNewNormalizer(t, eventNormalizerConfig{Provider: "generic", Format: "text", SchemaFingerprint: "generic-text-v1", MaxFrameBytes: 256, MaxPayloadBytes: 128})
	var events []RuntimeEvent
	for _, write := range writes {
		events = append(events, testFeed(t, n, write.channel, write.data)...)
	}
	return append(append(events, testFlush(t, n, "process.stdout")...), testFlush(t, n, "process.stderr")...)
}

func testAssertMixedEvents(t *testing.T, events []RuntimeEvent) {
	t.Helper()
	want := []struct{ channel, exact, prefix, suffix string }{
		{"process.stdout", "Привет 👋", "", ""}, {"process.stdout", "stdout-lone-cr", "", ""},
		{"process.stdout", "stdout-lone-lf", "", ""}, {"process.stderr", "stderr-one", "", ""},
		{"process.stdout", "interleaved-out", "", ""}, {"process.stderr", "", "bad", "SAFE_SUFFIX"},
		{"process.stdout", "финал 🚀", "", ""},
	}
	if len(events) != len(want) {
		t.Fatalf("events = %#v; want %d", events, len(want))
	}
	for i, expected := range want {
		event, text := events[i], testEventText(t, events[i])
		if event.Channel != expected.channel || event.Type != "command_output_delta" || event.Terminal || event.Truncated ||
			(expected.exact != "" && text != expected.exact) || (expected.prefix != "" && !strings.HasPrefix(text, expected.prefix)) ||
			(expected.suffix != "" && !strings.Contains(text, expected.suffix)) || !utf8.ValidString(text) || strings.ContainsAny(text, "\x00\x1b\r\n") {
			t.Fatalf("event[%d] = %#v text=%q", i, event, text)
		}
	}
}

func testProjectEvents(t *testing.T, events []RuntimeEvent) []testEventProjection {
	t.Helper()
	result := make([]testEventProjection, 0, len(events))
	for _, event := range events {
		result = append(result, testEventProjection{event.Channel, event.Type, string(testJSON(t, event.Payload)), event.Redacted, event.Truncated, event.Terminal})
	}
	return result
}

func testEqualProjection(t *testing.T, want, got []testEventProjection) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("semantic projection differs\nwant=%#v\ngot=%#v", want, got)
	}
}

func testWriteBytes(writes []testObservedWrite) int {
	total := 0
	for _, write := range writes {
		total += len(write.data)
	}
	return total
}

func testSplitWrites(writes []testObservedWrite, split int) []testObservedWrite {
	result, offset := make([]testObservedWrite, 0, len(writes)+1), 0
	for _, write := range writes {
		next := offset + len(write.data)
		if split > offset && split < next {
			at := split - offset
			result = append(result, testObservedWrite{write.channel, append([]byte(nil), write.data[:at]...)}, testObservedWrite{write.channel, append([]byte(nil), write.data[at:]...)})
		} else {
			result = append(result, testObservedWrite{write.channel, append([]byte(nil), write.data...)})
		}
		offset = next
	}
	return result
}

func testChunkWrites(writes []testObservedWrite, size func() int) []testObservedWrite {
	var result []testObservedWrite
	for _, write := range writes {
		for offset := 0; offset < len(write.data); {
			end := offset + size()
			if end > len(write.data) {
				end = len(write.data)
			}
			result = append(result, testObservedWrite{write.channel, append([]byte(nil), write.data[offset:end]...)})
			offset = end
		}
	}
	return result
}

func testRepresentations(t *testing.T, event RuntimeEvent) map[string]string {
	t.Helper()
	return map[string]string{"json": string(testJSON(t, event)), "debug": fmt.Sprintf("%+v", event), "error": fmt.Errorf("runtime event: %v", event).Error()}
}

func testNoForbidden(t *testing.T, name, value string, forbidden ...string) {
	t.Helper()
	for _, item := range forbidden {
		if strings.Contains(value, item) {
			t.Fatalf("%s contains forbidden %q in %s", name, item, value)
		}
	}
	if strings.ContainsAny(value, "\x00\x1b") {
		t.Fatalf("%s contains raw controls", name)
	}
}

func testNewNormalizer(t *testing.T, config eventNormalizerConfig) *eventNormalizer {
	t.Helper()
	n, err := newEventNormalizer(config)
	if err != nil {
		t.Fatalf("newEventNormalizer: %v", err)
	}
	return n
}

func testNewPump(t *testing.T, config eventPumpConfig) *eventPump {
	t.Helper()
	pump, err := newEventPump(config)
	if err != nil {
		t.Fatalf("newEventPump: %v", err)
	}
	t.Cleanup(pump.close)
	return pump
}

func testFeed(t *testing.T, n *eventNormalizer, channel string, chunk []byte) []RuntimeEvent {
	t.Helper()
	events, err := n.feed(channel, chunk)
	if err != nil {
		t.Fatalf("feed(%s,%d): %v", channel, len(chunk), err)
	}
	return events
}

func testFlush(t *testing.T, n *eventNormalizer, channel string) []RuntimeEvent {
	t.Helper()
	events, err := n.flush(channel)
	if err != nil {
		t.Fatalf("flush(%s): %v", channel, err)
	}
	return events
}

func testDelta(channel, text string) RuntimeEvent {
	return RuntimeEvent{Provider: "generic", Channel: channel, Type: "command_output_delta", Payload: map[string]any{"text": text}}
}

func testOrdinary(count int) []RuntimeEvent {
	result := make([]RuntimeEvent, count)
	for i := range result {
		result[i] = testOrdinaryEvent(i)
	}
	return result
}

func testOrdinaryEvent(index int) RuntimeEvent {
	switch index % 4 {
	case 0:
		return testDelta("process.stdout", "a")
	case 1:
		return testDelta("process.stderr", "b")
	case 2:
		return RuntimeEvent{Provider: "generic", Channel: "assistant", Type: "text_delta", Payload: map[string]any{"text": "c"}}
	default:
		return testDelta("file", "d")
	}
}

func testControl(kind string, terminal bool, status string) RuntimeEvent {
	return RuntimeEvent{Provider: "generic", Channel: "system", Type: kind, Payload: map[string]any{"status": status}, Terminal: terminal}
}

func testSizedControl(kind string, terminal bool, text string) RuntimeEvent {
	return RuntimeEvent{Provider: "generic", Channel: "system", Type: kind, Payload: map[string]any{"text": text}, Terminal: terminal}
}

func testAdmitPromptly(t *testing.T, pump *eventPump, name string, event RuntimeEvent) AdmissionResult {
	t.Helper()
	ready, start, result := make(chan struct{}), make(chan struct{}), make(chan AdmissionResult, 1)
	go func() { close(ready); <-start; result <- pump.admit(event) }()
	<-ready
	close(start)
	select {
	case got := <-result:
		return got
	case <-time.After(3 * time.Second):
		t.Fatalf("%s admission blocked", name)
		return AdmissionResult{}
	}
}

func testAdmissionStatus(result AdmissionResult) string { return fmt.Sprint(result.Status) }

func testStatus(t *testing.T, result AdmissionResult, want string) {
	t.Helper()
	if got := testAdmissionStatus(result); got != want {
		t.Fatalf("status=%q want=%q result=%#v", got, want, result)
	}
}

func testEventText(t *testing.T, event RuntimeEvent) string {
	t.Helper()
	return testString(t, event.Payload, "text")
}

func testString(t *testing.T, payload map[string]any, key string) string {
	t.Helper()
	value, ok := payload[key].(string)
	if !ok {
		t.Fatalf("payload[%q]=%T(%v)", key, payload[key], payload[key])
	}
	return value
}

func testUint(t *testing.T, payload map[string]any, key string) uint64 {
	t.Helper()
	switch value := payload[key].(type) {
	case int:
		return uint64(value)
	case int64:
		return uint64(value)
	case uint64:
		return value
	case float64:
		return uint64(value)
	default:
		t.Fatalf("payload[%q]=%T(%v)", key, payload[key], payload[key])
		return 0
	}
}

func testJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return data
}
