package workerruntime

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/thebtf/aimux/pkg/executor/redact"
)

const (
	defaultPumpMaxEvents     = 4_096
	defaultPumpMaxBytes      = 8 << 20
	defaultPumpReserveEvents = 128
	defaultPumpReserveBytes  = 1 << 20
	hardPumpMaxEvents        = 65_536
	hardPumpMaxBytes         = 64 << 20
)

type RuntimeEvent struct {
	Provider      string         `json:"provider"`
	Channel       string         `json:"channel"`
	Type          string         `json:"type"`
	Payload       map[string]any `json:"payload"`
	Redacted      bool           `json:"redacted"`
	Truncated     bool           `json:"truncated"`
	Terminal      bool           `json:"terminal"`
	IngestOrdinal uint64         `json:"ingest_ordinal"`

	internalGap bool
}

const (
	admissionAdmitted        AdmissionStatus = "admitted"
	admissionCoalesced       AdmissionStatus = "coalesced"
	admissionRejectedQuota   AdmissionStatus = "rejected_quota"
	admissionRejectedInvalid AdmissionStatus = "rejected_invalid"
	admissionRejectedClosed  AdmissionStatus = "rejected_closed"
)

type AdmissionStatus string

type AdmissionResult struct {
	Status          AdmissionStatus `json:"status"`
	IngestOrdinal   uint64          `json:"ingest_ordinal"`
	CoalescedEvents uint64          `json:"coalesced_events"`
	CoalescedBytes  uint64          `json:"coalesced_bytes"`
}

type eventNormalizerConfig struct {
	Provider          string
	Format            string
	SchemaFingerprint string
	MaxFrameBytes     int
	MaxPayloadBytes   int
}

// decodedFrame is the framing layer's complete, policy-neutral output.
type decodedFrame struct {
	raw        []byte
	byteLength uint64
	oversize   bool
}

type frameState struct {
	data       []byte
	frameBytes uint64
	oversize   bool
	skipLF     bool
}

// eventDecoder owns channel-local byte framing and nothing provider-specific.
type eventDecoder struct {
	structuredStdout bool
	maxFrameBytes    int
	channels         map[string]*frameState
}

// eventPolicy turns complete frames into bounded, sink-safe runtime events.
type eventPolicy struct {
	provider          string
	format            string
	schemaFingerprint string
	maxPayloadBytes   int
}

type eventNormalizer struct {
	mu      sync.Mutex
	decoder eventDecoder
	policy  eventPolicy
}

func newEventNormalizer(config eventNormalizerConfig) (*eventNormalizer, error) {
	config.Provider = strings.TrimSpace(config.Provider)
	config.Format = strings.ToLower(strings.TrimSpace(config.Format))
	if config.Provider == "" {
		return nil, errors.New("event normalizer provider must not be blank")
	}
	if config.Format != "text" && config.Format != "jsonl" {
		return nil, fmt.Errorf("unsupported event format %q", config.Format)
	}
	if config.MaxFrameBytes <= 0 {
		return nil, errors.New("event frame limit must be positive")
	}
	if config.MaxPayloadBytes < 64 {
		return nil, errors.New("event payload limit must be at least 64 bytes")
	}
	return &eventNormalizer{
		decoder: eventDecoder{
			structuredStdout: config.Format == "jsonl",
			maxFrameBytes:    config.MaxFrameBytes,
			channels:         make(map[string]*frameState),
		},
		policy: eventPolicy{
			provider:          config.Provider,
			format:            config.Format,
			schemaFingerprint: config.SchemaFingerprint,
			maxPayloadBytes:   config.MaxPayloadBytes,
		},
	}, nil
}

func (normalizer *eventNormalizer) feed(channel string, chunk []byte) ([]RuntimeEvent, error) {
	channel, err := canonicalEventChannel(channel)
	if err != nil {
		return nil, err
	}
	normalizer.mu.Lock()
	defer normalizer.mu.Unlock()

	events := make([]RuntimeEvent, 0, 1)
	normalizer.decoder.feed(channel, chunk, func(frame decodedFrame) {
		events = append(events, normalizer.policy.normalizeFrame(channel, frame)...)
	})
	return events, nil
}

func (normalizer *eventNormalizer) flush(channel string) ([]RuntimeEvent, error) {
	channel, err := canonicalEventChannel(channel)
	if err != nil {
		return nil, err
	}
	normalizer.mu.Lock()
	defer normalizer.mu.Unlock()
	frame, ok := normalizer.decoder.flush(channel)
	if !ok {
		return nil, nil
	}
	return normalizer.policy.normalizeFrame(channel, frame), nil
}

func (decoder *eventDecoder) feed(channel string, chunk []byte, emit func(decodedFrame)) {
	state := decoder.channels[channel]
	if state == nil {
		state = &frameState{}
		decoder.channels[channel] = state
	}
	structured := decoder.structuredStdout && channel == "process.stdout"
	for _, value := range chunk {
		if !structured {
			if state.skipLF {
				state.skipLF = false
				if value == '\n' {
					continue
				}
			}
			if value == '\r' || value == '\n' {
				if frame, ok := decoder.finishFrame(state); ok {
					emit(frame)
				}
				state.skipLF = value == '\r'
				continue
			}
		} else if value == '\n' {
			if !state.oversize && len(state.data) > 0 && state.data[len(state.data)-1] == '\r' {
				state.data = state.data[:len(state.data)-1]
				state.frameBytes--
			}
			if frame, ok := decoder.finishFrame(state); ok {
				emit(frame)
			}
			continue
		}
		decoder.appendFrameByte(state, value)
	}
}

func (decoder *eventDecoder) flush(channel string) (decodedFrame, bool) {
	state := decoder.channels[channel]
	if state == nil {
		return decodedFrame{}, false
	}
	state.skipLF = false
	frame, ok := decoder.finishFrame(state)
	delete(decoder.channels, channel)
	return frame, ok
}

func (decoder *eventDecoder) appendFrameByte(state *frameState, value byte) {
	state.frameBytes++
	if state.oversize {
		return
	}
	if state.frameBytes > uint64(decoder.maxFrameBytes) {
		state.oversize = true
		state.data = nil
		return
	}
	state.data = append(state.data, value)
}

func (decoder *eventDecoder) finishFrame(state *frameState) (decodedFrame, bool) {
	frame := decodedFrame{raw: state.data, byteLength: state.frameBytes, oversize: state.oversize}
	state.data = nil
	state.frameBytes = 0
	state.oversize = false
	return frame, frame.byteLength != 0
}

func canonicalEventChannel(channel string) (string, error) {
	switch strings.TrimSpace(channel) {
	case "process.stdout":
		return "process.stdout", nil
	case "process.stderr":
		return "process.stderr", nil
	case "":
		return "", errors.New("event channel must not be blank")
	default:
		return "", fmt.Errorf("unsupported event channel %q", safeMetadata(channel, 80))
	}
}

func (policy *eventPolicy) normalizeFrame(channel string, frame decodedFrame) []RuntimeEvent {
	if frame.oversize {
		return []RuntimeEvent{{
			Provider:  policy.provider,
			Channel:   channel,
			Type:      "output_truncated",
			Payload:   map[string]any{"reason": "frame_limit", "dropped_events": uint64(1), "dropped_bytes": frame.byteLength},
			Truncated: true,
		}}
	}
	raw := frame.raw
	if policy.format == "text" || channel != "process.stdout" {
		text, wasRedacted, wasTruncated := policy.prepareText(string(raw))
		return []RuntimeEvent{{
			Provider:  policy.provider,
			Channel:   channel,
			Type:      "command_output_delta",
			Payload:   map[string]any{"text": text},
			Redacted:  wasRedacted,
			Truncated: wasTruncated,
		}}
	}

	var envelope map[string]any
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return []RuntimeEvent{policy.unknownEvent("invalid_json", raw)}
	}
	provider := strings.ToLower(policy.provider)
	switch provider {
	case "codex":
		return policy.normalizeCodex(envelope, raw)
	case "grok":
		return policy.normalizeGrok(envelope, raw)
	default:
		return []RuntimeEvent{policy.unknownEvent(providerMethod(envelope), raw)}
	}
}

func (policy *eventPolicy) normalizeCodex(envelope map[string]any, raw []byte) []RuntimeEvent {
	method := stringField(envelope, "method")
	eventType := stringField(envelope, "type")
	item := mapField(envelope, "item")
	itemType := stringField(item, "type")
	if containsPrivateKind(method) || containsPrivateKind(eventType) || containsPrivateKind(itemType) {
		return nil
	}
	if eventType != "item.completed" || itemType != "agent_message" {
		return []RuntimeEvent{policy.unknownEvent(providerMethod(envelope), raw)}
	}
	text, ok := item["text"].(string)
	if !ok {
		return []RuntimeEvent{policy.unknownEvent(providerMethod(envelope), raw)}
	}
	return []RuntimeEvent{policy.assistantEvent(text, containsSensitiveValue(envelope))}
}

func (policy *eventPolicy) normalizeGrok(envelope map[string]any, raw []byte) []RuntimeEvent {
	method := stringField(envelope, "method")
	if method == "_x.ai/mcp/servers_updated" {
		return []RuntimeEvent{policy.unknownEvent(method, raw)}
	}
	params := mapField(envelope, "params")
	update := mapField(params, "update")
	updateType := stringField(update, "sessionUpdate")
	if containsPrivateKind(updateType) {
		return nil
	}
	if method != "session/update" || updateType != "agent_message_chunk" {
		return []RuntimeEvent{policy.unknownEvent(providerMethod(envelope), raw)}
	}
	text, ok := update["content"].(string)
	if !ok {
		return []RuntimeEvent{policy.unknownEvent(providerMethod(envelope), raw)}
	}
	return []RuntimeEvent{policy.assistantEvent(text, containsSensitiveValue(envelope))}
}

func (policy *eventPolicy) assistantEvent(rawText string, structuralRedaction bool) RuntimeEvent {
	text, valueRedaction, truncated := policy.prepareText(rawText)
	return RuntimeEvent{
		Provider:  policy.provider,
		Channel:   "assistant",
		Type:      "text_delta",
		Payload:   map[string]any{"text": text},
		Redacted:  structuralRedaction || valueRedaction,
		Truncated: truncated,
	}
}

func (policy *eventPolicy) prepareText(rawText string) (string, bool, bool) {
	valid := strings.ToValidUTF8(rawText, "\uFFFD")
	safe := strings.Map(func(value rune) rune {
		if unicode.IsControl(value) {
			return -1
		}
		return value
	}, valid)
	redactedText := redact.RedactSecrets(safe)
	bounded, truncated := boundTextPayload(redactedText, policy.maxPayloadBytes)
	return bounded, redactedText != safe, truncated
}

func (policy *eventPolicy) unknownEvent(method string, raw []byte) RuntimeEvent {
	digest := sha256.Sum256(raw)
	method = safeMetadata(method, 80)
	fingerprint := safeMetadata(policy.schemaFingerprint, 96)
	payload := map[string]any{
		"provider_method":    method,
		"schema_fingerprint": fingerprint,
		"byte_length":        uint64(len(raw)),
		"diagnostic_hash":    hex.EncodeToString(digest[:]),
	}
	if jsonSize(payload) > policy.maxPayloadBytes {
		methodHash := sha256.Sum256([]byte(method))
		fingerprintHash := sha256.Sum256([]byte(fingerprint))
		payload = map[string]any{
			"b": uint64(len(raw)),
			"m": base64.RawURLEncoding.EncodeToString(methodHash[:6]),
			"n": uint64(1),
			"s": base64.RawURLEncoding.EncodeToString(fingerprintHash[:6]),
		}
	}
	return RuntimeEvent{
		Provider: policy.provider,
		Channel:  "system",
		Type:     "provider_event_unknown",
		Payload:  payload,
		Redacted: containsSecretText(string(raw)),
	}
}

func providerMethod(envelope map[string]any) string {
	if method := stringField(envelope, "method"); method != "" {
		return method
	}
	if eventType := stringField(envelope, "type"); eventType != "" {
		return eventType
	}
	return "unknown"
}

func mapField(value map[string]any, key string) map[string]any {
	if value == nil {
		return nil
	}
	field, _ := value[key].(map[string]any)
	return field
}

func stringField(value map[string]any, key string) string {
	if value == nil {
		return ""
	}
	field, _ := value[key].(string)
	return field
}

func containsPrivateKind(value string) bool {
	lower := strings.ToLower(value)
	return strings.Contains(lower, "reasoning") || strings.Contains(lower, "thought")
}

func containsSensitiveValue(value any) bool {
	switch typed := value.(type) {
	case map[string]any:
		for key, item := range typed {
			if sensitiveKey(key) || containsSensitiveValue(item) {
				return true
			}
		}
	case []any:
		for _, item := range typed {
			if containsSensitiveValue(item) {
				return true
			}
		}
	case string:
		return containsSecretText(typed)
	}
	return false
}

func sensitiveKey(key string) bool {
	normalized := strings.NewReplacer("_", "", "-", "", ".", "").Replace(strings.ToLower(key))
	for _, forbidden := range []string{"apikey", "authorization", "accesstoken", "refreshtoken", "password", "secret", "credential", "thought", "reasoning"} {
		if strings.Contains(normalized, forbidden) {
			return true
		}
	}
	return normalized == "token"
}

func containsSecretText(value string) bool {
	return redact.RedactSecrets(value) != value
}

func safeMetadata(value string, limit int) string {
	value = strings.ToValidUTF8(value, "\uFFFD")
	value = strings.Map(func(item rune) rune {
		if unicode.IsControl(item) {
			return -1
		}
		return item
	}, redact.RedactSecrets(value))
	return truncateUTF8(value, limit)
}

func boundTextPayload(text string, limit int) (string, bool) {
	encodedBytes := len(`{"text":""}`)
	for offset, value := range text {
		width := utf8.RuneLen(value)
		encodedWidth := width
		switch value {
		case '"', '\\':
			encodedWidth = 2
		case '<', '>', '&', '\u2028', '\u2029':
			encodedWidth = 6
		default:
			if value < 0x20 {
				encodedWidth = 6
			}
		}
		if encodedBytes+encodedWidth > limit {
			return text[:offset], true
		}
		encodedBytes += encodedWidth
	}
	return text, false
}

func truncateUTF8(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if len(value) <= limit {
		return value
	}
	value = value[:limit]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}

func jsonSize(value any) int {
	data, err := json.Marshal(value)
	if err != nil {
		return int(^uint(0) >> 1)
	}
	return len(data)
}

type eventPumpConfig struct {
	MaxEvents            int
	MaxBytes             int
	ControlReserveEvents int
	ControlReserveBytes  int
}

func defaultEventPumpConfig() eventPumpConfig {
	return eventPumpConfig{
		MaxEvents:            defaultPumpMaxEvents,
		MaxBytes:             defaultPumpMaxBytes,
		ControlReserveEvents: defaultPumpReserveEvents,
		ControlReserveBytes:  defaultPumpReserveBytes,
	}
}

// eventPump owns admission, coalescing, quotas, ordering, and queue storage.
type eventPump struct {
	mu      sync.Mutex
	config  eventPumpConfig
	entries []pumpEntry
	head    int
	closed  bool
	pending *pumpEntry

	nextOrdinal    uint64
	totalBytes     int
	ordinaryEvents int
	ordinaryBytes  int
}

type pumpEntry struct {
	event    RuntimeEvent
	size     int
	control  bool
	textOnly bool
	text     []byte
}

func newEventPump(config eventPumpConfig) (*eventPump, error) {
	if config.MaxEvents <= 0 || config.MaxEvents > hardPumpMaxEvents {
		return nil, fmt.Errorf("event limit must be within 1..%d", hardPumpMaxEvents)
	}
	if config.MaxBytes <= 0 || config.MaxBytes > hardPumpMaxBytes {
		return nil, fmt.Errorf("byte limit must be within 1..%d", hardPumpMaxBytes)
	}
	if config.ControlReserveEvents <= 0 || config.ControlReserveEvents >= config.MaxEvents {
		return nil, errors.New("control event reserve must be positive and smaller than the event limit")
	}
	if config.ControlReserveBytes <= 0 || config.ControlReserveBytes >= config.MaxBytes {
		return nil, errors.New("control byte reserve must be positive and smaller than the byte limit")
	}
	return &eventPump{config: config}, nil
}

func (pump *eventPump) admit(event RuntimeEvent) AdmissionResult {
	pump.mu.Lock()
	defer pump.mu.Unlock()
	if pump.closed {
		return AdmissionResult{Status: admissionRejectedClosed}
	}
	pump.materializePendingGap()

	owned, size, err := ownRuntimeEvent(event)
	if err != nil {
		return AdmissionResult{Status: admissionRejectedInvalid}
	}
	control := isControlEvent(owned)
	if shouldBoundStatus(owned, size, pump.config.ControlReserveBytes) {
		owned, size, err = boundedStatusEvent(owned, size)
		if err != nil {
			return AdmissionResult{Status: admissionRejectedInvalid}
		}
	}

	if result, handled := pump.coalesce(owned, size, control); handled {
		return result
	}
	if !pump.hasCapacity(control, size, 1) {
		if !control {
			pump.recordDrop(owned.Provider, uint64(size))
		}
		return AdmissionResult{Status: admissionRejectedQuota}
	}
	pump.nextOrdinal++
	owned.IngestOrdinal = pump.nextOrdinal
	entry := pumpEntry{event: owned, size: size, control: control}
	if text, ok := exactTextPayload(owned.Payload); ok && isDeltaEvent(owned) {
		entry.textOnly = true
		entry.text = append([]byte(nil), text...)
		entry.event.Payload["text"] = ""
	}
	pump.entries = append(pump.entries, entry)
	pump.totalBytes += size
	if !control {
		pump.ordinaryEvents++
		pump.ordinaryBytes += size
	}
	return AdmissionResult{Status: admissionAdmitted, IngestOrdinal: pump.nextOrdinal}
}

func (pump *eventPump) drain(limit int) []RuntimeEvent {
	pump.mu.Lock()
	defer pump.mu.Unlock()
	queued := len(pump.entries) - pump.head
	available := queued
	if pump.pending != nil {
		available++
	}
	if limit <= 0 || available == 0 {
		return nil
	}
	if limit > available {
		limit = available
	}
	result := make([]RuntimeEvent, limit)
	drainEntries := limit
	if drainEntries > queued {
		drainEntries = queued
	}
	for index := 0; index < drainEntries; index++ {
		entryIndex := pump.head + index
		entry := &pump.entries[entryIndex]
		if entry.textOnly {
			entry.event.Payload["text"] = string(entry.text)
		}
		result[index] = entry.event
		pump.totalBytes -= entry.size
		if !entry.control {
			pump.ordinaryEvents--
			pump.ordinaryBytes -= entry.size
		}
		pump.entries[entryIndex] = pumpEntry{}
	}
	pump.head += drainEntries
	if drainEntries < limit && pump.pending != nil {
		result[drainEntries] = pump.pending.event
		pump.pending = nil
	}
	pump.compact()
	pump.materializePendingGap()
	return result
}

func (pump *eventPump) close() {
	pump.mu.Lock()
	pump.closed = true
	pump.mu.Unlock()
}

func (pump *eventPump) hasCapacity(control bool, byteDelta, eventDelta int) bool {
	if len(pump.entries)-pump.head+eventDelta > pump.config.MaxEvents || pump.totalBytes+byteDelta > pump.config.MaxBytes {
		return false
	}
	if control {
		return true
	}
	return pump.ordinaryEvents+eventDelta <= pump.config.MaxEvents-pump.config.ControlReserveEvents &&
		pump.ordinaryBytes+byteDelta <= pump.config.MaxBytes-pump.config.ControlReserveBytes
}

func (pump *eventPump) coalesce(event RuntimeEvent, size int, control bool) (AdmissionResult, bool) {
	if len(pump.entries) == pump.head || control || pump.pending != nil {
		return AdmissionResult{}, false
	}
	tail := &pump.entries[len(pump.entries)-1]
	if text, ok := exactTextPayload(event.Payload); ok && isDeltaEvent(event) && tail.textOnly &&
		tail.event.Provider == event.Provider && tail.event.Channel == event.Channel && tail.event.Type == event.Type &&
		!tail.event.Terminal && !tail.event.internalGap {
		if !pump.hasCapacity(false, size, 0) {
			pump.recordDrop(event.Provider, uint64(size))
			return AdmissionResult{Status: admissionRejectedQuota}, true
		}
		pump.nextOrdinal++
		tail.text = append(tail.text, text...)
		tail.event.Redacted = tail.event.Redacted || event.Redacted
		tail.event.Truncated = tail.event.Truncated || event.Truncated
		tail.event.IngestOrdinal = pump.nextOrdinal
		tail.size += size
		pump.totalBytes += size
		pump.ordinaryBytes += size
		return AdmissionResult{Status: admissionCoalesced, IngestOrdinal: pump.nextOrdinal, CoalescedEvents: 1, CoalescedBytes: uint64(size)}, true
	}
	if tail.event.Type != "provider_event_unknown" || event.Type != tail.event.Type || tail.event.Provider != event.Provider ||
		unknownSignature(tail.event.Payload, "provider_method", "m") != unknownSignature(event.Payload, "provider_method", "m") ||
		unknownSignature(tail.event.Payload, "schema_fingerprint", "s") != unknownSignature(event.Payload, "schema_fingerprint", "s") {
		return AdmissionResult{}, false
	}
	aggregate := cloneFlatPayload(tail.event.Payload)
	if _, compact := aggregate["m"]; compact {
		aggregate["n"] = payloadUint64Any(tail.event.Payload, 1, "n", "occurrences", "occurrence_count") + payloadUint64Any(event.Payload, 1, "n", "occurrences", "occurrence_count")
		aggregate["b"] = payloadUint64Any(tail.event.Payload, 0, "b", "byte_length", "byte_count") + payloadUint64Any(event.Payload, 0, "b", "byte_length", "byte_count")
	} else {
		aggregate["occurrences"] = payloadUint64Any(tail.event.Payload, 1, "occurrences", "occurrence_count") + payloadUint64Any(event.Payload, 1, "occurrences", "occurrence_count")
		aggregate["byte_length"] = payloadUint64Any(tail.event.Payload, 0, "byte_length", "byte_count") + payloadUint64Any(event.Payload, 0, "byte_length", "byte_count")
		delete(aggregate, "occurrence_count")
		delete(aggregate, "byte_count")
	}
	aggregateSize, err := retainedPayloadSize(aggregate)
	if err != nil {
		return AdmissionResult{Status: admissionRejectedInvalid}, true
	}
	delta := aggregateSize - tail.size
	if !pump.hasCapacity(false, delta, 0) {
		pump.recordDrop(event.Provider, uint64(size))
		return AdmissionResult{Status: admissionRejectedQuota}, true
	}
	pump.nextOrdinal++
	tail.event.Payload = aggregate
	tail.event.Redacted = tail.event.Redacted || event.Redacted
	tail.event.Truncated = tail.event.Truncated || event.Truncated
	tail.event.IngestOrdinal = pump.nextOrdinal
	tail.size = aggregateSize
	pump.totalBytes += delta
	pump.ordinaryBytes += delta
	return AdmissionResult{Status: admissionCoalesced, IngestOrdinal: pump.nextOrdinal, CoalescedEvents: 1, CoalescedBytes: uint64(size)}, true
}

func (pump *eventPump) recordDrop(provider string, size uint64) {
	if len(pump.entries) > pump.head {
		tail := &pump.entries[len(pump.entries)-1]
		if tail.event.internalGap {
			tail.event.Payload["dropped_events"] = payloadUint64(tail.event.Payload, "dropped_events", 0) + 1
			tail.event.Payload["dropped_bytes"] = payloadUint64(tail.event.Payload, "dropped_bytes", 0) + size
			return
		}
	}
	if pump.pending != nil {
		pump.pending.event.Payload["dropped_events"] = payloadUint64(pump.pending.event.Payload, "dropped_events", 0) + 1
		pump.pending.event.Payload["dropped_bytes"] = payloadUint64(pump.pending.event.Payload, "dropped_bytes", 0) + size
		return
	}
	gap := RuntimeEvent{
		Provider:    provider,
		Channel:     "system",
		Type:        "output_truncated",
		Payload:     map[string]any{"reason": "admission_quota", "dropped_events": uint64(1), "dropped_bytes": size},
		Truncated:   true,
		internalGap: true,
	}
	pump.nextOrdinal++
	gap.IngestOrdinal = pump.nextOrdinal
	entry := pumpEntry{event: gap, control: true}
	if pump.hasCapacity(true, 0, 1) {
		pump.entries = append(pump.entries, entry)
		return
	}
	pump.pending = &entry
}

func (pump *eventPump) materializePendingGap() {
	if pump.pending == nil || !pump.hasCapacity(true, 0, 1) {
		return
	}
	pump.entries = append(pump.entries, *pump.pending)
	pump.pending = nil
}

func (pump *eventPump) compact() {
	if pump.head == len(pump.entries) {
		pump.entries = nil
		pump.head = 0
		return
	}
	if pump.head >= 1024 && pump.head*2 >= len(pump.entries) {
		live := copy(pump.entries, pump.entries[pump.head:])
		clear(pump.entries[live:])
		pump.entries = pump.entries[:live]
		pump.head = 0
	}
}

func ownRuntimeEvent(event RuntimeEvent) (RuntimeEvent, int, error) {
	event.Provider = strings.Clone(event.Provider)
	event.Channel = strings.Clone(event.Channel)
	event.Type = strings.Clone(event.Type)
	event.IngestOrdinal = 0
	event.internalGap = false
	payload, size, err := ownPayload(event.Payload)
	if err != nil {
		return RuntimeEvent{}, 0, err
	}
	event.Payload = payload
	return event, size, nil
}

func ownPayload(payload map[string]any) (map[string]any, int, error) {
	if len(payload) == 1 {
		for _, key := range []string{"text", "status"} {
			if value, ok := payload[key].(string); ok && utf8.ValidString(value) {
				value = strings.Clone(value)
				return map[string]any{key: value}, len(value), nil
			}
		}
	}
	if err := validateJSONGraph(reflect.ValueOf(payload), make(map[jsonVisit]struct{})); err != nil {
		return nil, 0, err
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, 0, err
	}
	var owned map[string]any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&owned); err != nil {
		return nil, 0, err
	}
	normalizeJSONNumbers(owned)
	return owned, len(data), nil
}

func normalizeJSONNumbers(value any) {
	switch typed := value.(type) {
	case map[string]any:
		for key, item := range typed {
			if number, ok := item.(json.Number); ok {
				typed[key] = canonicalJSONNumber(number)
				continue
			}
			normalizeJSONNumbers(item)
		}
	case []any:
		for index, item := range typed {
			if number, ok := item.(json.Number); ok {
				typed[index] = canonicalJSONNumber(number)
				continue
			}
			normalizeJSONNumbers(item)
		}
	}
}

func canonicalJSONNumber(number json.Number) any {
	raw := number.String()
	if value, err := number.Int64(); err == nil && strconv.FormatInt(value, 10) == raw {
		return value
	}
	if value, err := strconv.ParseUint(raw, 10, 64); err == nil && strconv.FormatUint(value, 10) == raw {
		return value
	}
	return number
}

type jsonVisit struct {
	kind    reflect.Kind
	typeOf  reflect.Type
	pointer uintptr
}

func validateJSONGraph(value reflect.Value, visiting map[jsonVisit]struct{}) error {
	if !value.IsValid() {
		return nil
	}
	if value.Kind() == reflect.Interface {
		if value.IsNil() {
			return nil
		}
		return validateJSONGraph(value.Elem(), visiting)
	}
	switch value.Kind() {
	case reflect.Func, reflect.Chan, reflect.Complex64, reflect.Complex128, reflect.UnsafePointer:
		return fmt.Errorf("unsupported JSON payload value %s", value.Kind())
	case reflect.Map, reflect.Pointer, reflect.Slice:
		if value.IsNil() {
			return nil
		}
		visit := jsonVisit{kind: value.Kind(), typeOf: value.Type(), pointer: value.Pointer()}
		if _, seen := visiting[visit]; seen {
			return errors.New("cyclic JSON payload")
		}
		visiting[visit] = struct{}{}
		defer delete(visiting, visit)
	}
	switch value.Kind() {
	case reflect.Map:
		iterator := value.MapRange()
		for iterator.Next() {
			if err := validateJSONGraph(iterator.Value(), visiting); err != nil {
				return err
			}
		}
	case reflect.Pointer:
		return validateJSONGraph(value.Elem(), visiting)
	case reflect.Slice, reflect.Array:
		for index := 0; index < value.Len(); index++ {
			if err := validateJSONGraph(value.Index(index), visiting); err != nil {
				return err
			}
		}
	case reflect.Struct:
		for index := 0; index < value.NumField(); index++ {
			field := value.Type().Field(index)
			if field.PkgPath == "" {
				if err := validateJSONGraph(value.Field(index), visiting); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func retainedPayloadSize(payload map[string]any) (int, error) {
	if text, ok := exactTextPayload(payload); ok {
		return len(text), nil
	}
	if len(payload) == 1 {
		if status, ok := payload["status"].(string); ok {
			return len(status), nil
		}
	}
	data, err := json.Marshal(payload)
	return len(data), err
}

func shouldBoundStatus(event RuntimeEvent, size, reserve int) bool {
	return event.Type == "status" && len(event.Payload) > 1 && size > reserve
}

func boundedStatusEvent(event RuntimeEvent, originalSize int) (RuntimeEvent, int, error) {
	status, ok := event.Payload["status"].(string)
	if !ok {
		return RuntimeEvent{}, 0, errors.New("oversized status payload lacks a string status")
	}
	coreSize := jsonSize(map[string]any{"status": status})
	if coreSize > originalSize {
		return RuntimeEvent{}, 0, errors.New("status payload accounting is inconsistent")
	}
	event.Payload = map[string]any{
		"status":                 strings.Clone(status),
		"original_payload_bytes": uint64(originalSize),
		"omitted_bytes":          uint64(originalSize - coreSize),
	}
	event.Truncated = true
	size, err := retainedPayloadSize(event.Payload)
	return event, size, err
}

func exactTextPayload(payload map[string]any) (string, bool) {
	if len(payload) != 1 {
		return "", false
	}
	text, ok := payload["text"].(string)
	return text, ok
}

func unknownSignature(payload map[string]any, fullKey, compactKey string) string {
	if value, ok := payload[fullKey].(string); ok {
		return "full:" + value
	}
	value, _ := payload[compactKey].(string)
	return "compact:" + value
}

func payloadUint64(payload map[string]any, key string, fallback uint64) uint64 {
	switch value := payload[key].(type) {
	case uint64:
		return value
	case int:
		if value >= 0 {
			return uint64(value)
		}
	case int64:
		if value >= 0 {
			return uint64(value)
		}
	case float64:
		if value >= 0 {
			return uint64(value)
		}
	}
	return fallback
}

func payloadUint64Any(payload map[string]any, fallback uint64, keys ...string) uint64 {
	for _, key := range keys {
		if _, exists := payload[key]; exists {
			return payloadUint64(payload, key, fallback)
		}
	}
	return fallback
}

func cloneFlatPayload(payload map[string]any) map[string]any {
	clone := make(map[string]any, len(payload)+1)
	for key, value := range payload {
		clone[key] = value
	}
	return clone
}

func isControlEvent(event RuntimeEvent) bool {
	if event.Terminal {
		return true
	}
	switch event.Type {
	case "terminal", "error", "status", "approval_requested", "input_requested", "output_truncated":
		return true
	default:
		return false
	}
}

func isDeltaEvent(event RuntimeEvent) bool {
	return event.Type == "command_output_delta" || event.Type == "text_delta"
}
