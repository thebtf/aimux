package workerruntime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

type t019FailingSink struct {
	mu       sync.Mutex
	attempts int
}

func (sink *t019FailingSink) AppendRuntimeEvents(context.Context, []RuntimeEvent) error {
	sink.mu.Lock()
	sink.attempts++
	sink.mu.Unlock()
	return errors.New("SQLITE_BUSY: injected outage")
}

func (sink *t019FailingSink) Checkpoint(context.Context) error { return nil }

func (sink *t019FailingSink) Attempts() int {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	return sink.attempts
}

func TestEventWriter_RetryExhaustionIsBoundedAndExplicit(t *testing.T) {
	sink := &t019FailingSink{}
	var elapsed time.Duration
	config := DefaultEventWriterConfig(sink)
	config.Now = func() time.Time { return time.Unix(0, 0).Add(elapsed) }
	config.Wait = func(_ context.Context, delay time.Duration) error {
		elapsed += delay
		return nil
	}
	writer, err := NewEventWriter(config)
	if err != nil {
		t.Fatalf("NewEventWriter: %v", err)
	}
	if got := writer.Admit(testDelta("process.stdout", "accepted")); got.Status != admissionAdmitted {
		t.Fatalf("Admit status = %q, want %q", got.Status, admissionAdmitted)
	}

	err = writer.CloseAndFlush(context.Background())
	if !errors.Is(err, ErrArtifactSinkUnavailable) {
		t.Fatalf("CloseAndFlush error = %v, want ErrArtifactSinkUnavailable", err)
	}
	if got := sink.Attempts(); got != 8 {
		t.Fatalf("sink attempts = %d, want 8", got)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("retry elapsed = %v, want <= 5s", elapsed)
	}
	metrics := writer.Metrics()
	if metrics.RetryAttempts != 8 || metrics.WriterFailures != 1 {
		t.Fatalf("metrics = %#v, want 8 retry attempts and one failure", metrics)
	}
	if got := writer.Admit(testDelta("process.stdout", "late")); got.Status != admissionRejectedClosed {
		t.Fatalf("late admission = %q, want %q", got.Status, admissionRejectedClosed)
	}
}

type t019RecordingSink struct {
	mu          sync.Mutex
	batches     [][]RuntimeEvent
	checkpoints int
	err         error
}

type t019NeverTimer struct {
	ch chan time.Time
}

func (timer t019NeverTimer) C() <-chan time.Time { return timer.ch }
func (t019NeverTimer) Stop() bool                { return true }

func TestEventWriter_CoalescedEntryNeverExceedsBatchByteLimit(t *testing.T) {
	const chunks = 80
	sink := &t019RecordingSink{}
	config := DefaultEventWriterConfig(sink)
	config.NewTimer = func(time.Duration) EventWriterTimer {
		return t019NeverTimer{ch: make(chan time.Time)}
	}
	writer, err := NewEventWriter(config)
	if err != nil {
		t.Fatalf("NewEventWriter: %v", err)
	}
	defer func() { _ = writer.CloseAndFlush(context.Background()) }()

	chunk := strings.Repeat("x", 64<<10)
	for index := 0; index < chunks; index++ {
		result := writer.Admit(RuntimeEvent{
			Provider: "codex",
			Channel:  "process.stdout",
			Type:     "command_output_delta",
			Payload:  map[string]any{"text": chunk},
		})
		if result.Status != admissionAdmitted && result.Status != admissionCoalesced {
			t.Fatalf("admission %d = %#v; individually bounded output must remain drainable", index, result)
		}
	}

	if err := writer.CloseAndFlush(context.Background()); err != nil {
		t.Fatalf("CloseAndFlush rejected individually bounded output: %v", err)
	}
	metrics := writer.Metrics()
	if metrics.MaxBatchBytes > defaultWriterBatchBytes {
		t.Fatalf("max batch bytes = %d, want <= %d", metrics.MaxBatchBytes, defaultWriterBatchBytes)
	}

	batches, _ := sink.Snapshot()
	var persistedBytes int
	for batchIndex, batch := range batches {
		var batchBytes int
		for _, event := range batch {
			if event.Provider != "codex" {
				t.Fatalf("batch %d provider = %q, want codex", batchIndex, event.Provider)
			}
			text, ok := event.Payload["text"].(string)
			if !ok {
				t.Fatalf("batch %d event payload = %#v, want text", batchIndex, event.Payload)
			}
			persistedBytes += len(text)
			_, size, err := ownRuntimeEvent(event)
			if err != nil {
				t.Fatalf("ownRuntimeEvent: %v", err)
			}
			batchBytes += size
		}
		if batchBytes > defaultWriterBatchBytes {
			t.Fatalf("batch %d bytes = %d, want <= %d", batchIndex, batchBytes, defaultWriterBatchBytes)
		}
	}
	if persistedBytes != chunks*len(chunk) {
		t.Fatalf("persisted bytes = %d, want %d", persistedBytes, chunks*len(chunk))
	}
}

func (sink *t019RecordingSink) AppendRuntimeEvents(_ context.Context, batch []RuntimeEvent) error {
	if sink.err != nil {
		return sink.err
	}
	owned := make([]RuntimeEvent, len(batch))
	for i, event := range batch {
		clone, _, err := ownRuntimeEvent(event)
		if err != nil {
			return err
		}
		owned[i] = clone
	}
	sink.mu.Lock()
	sink.batches = append(sink.batches, owned)
	sink.mu.Unlock()
	return nil
}

func (sink *t019RecordingSink) Checkpoint(context.Context) error {
	sink.mu.Lock()
	sink.checkpoints++
	sink.mu.Unlock()
	return nil
}

func (sink *t019RecordingSink) Snapshot() ([][]RuntimeEvent, int) {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	batches := make([][]RuntimeEvent, len(sink.batches))
	for i := range sink.batches {
		batches[i] = append([]RuntimeEvent(nil), sink.batches[i]...)
	}
	return batches, sink.checkpoints
}

func TestEventWriter_EightTasks100000EventsStayBoundedAndRetainTerminal(t *testing.T) {
	const (
		tasks         = 8
		eventsPerTask = 12_500
	)
	type taskHarness struct {
		writer *EventWriter
		sink   *t019RecordingSink
	}
	harnesses := make([]taskHarness, tasks)
	for i := range harnesses {
		sink := &t019RecordingSink{}
		writer, err := NewEventWriter(DefaultEventWriterConfig(sink))
		if err != nil {
			t.Fatalf("writer[%d]: %v", i, err)
		}
		harnesses[i] = taskHarness{writer: writer, sink: sink}
	}

	var producers sync.WaitGroup
	producers.Add(tasks)
	for taskIndex := range harnesses {
		go func(taskIndex int) {
			defer producers.Done()
			writer := harnesses[taskIndex].writer
			provider := fmt.Sprintf("provider-%d", taskIndex)
			for eventIndex := 0; eventIndex < eventsPerTask-1; eventIndex++ {
				event := testDelta("process.stdout", "x")
				event.Provider = provider
				result := writer.Admit(event)
				if result.Status != admissionAdmitted && result.Status != admissionCoalesced {
					t.Errorf("task %d event %d admission = %#v", taskIndex, eventIndex, result)
					return
				}
			}
			terminal := testControl("terminal", true, "completed")
			terminal.Provider = provider
			if result := writer.Admit(terminal); result.Status != admissionAdmitted {
				t.Errorf("task %d terminal admission = %#v", taskIndex, result)
			}
		}(taskIndex)
	}
	producers.Wait()

	var accepted uint64
	var peakEvents, peakBytes, maxBatchEvents, maxBatchBytes int
	var maxBatchLatency, maxBatchP99 time.Duration
	for taskIndex, harness := range harnesses {
		if err := harness.writer.CloseAndFlush(context.Background()); err != nil {
			t.Fatalf("task %d CloseAndFlush: %v", taskIndex, err)
		}
		metrics := harness.writer.Metrics()
		accepted += metrics.AdmittedEvents + metrics.CoalescedEvents
		if metrics.PeakQueueEvents > peakEvents {
			peakEvents = metrics.PeakQueueEvents
		}
		if metrics.PeakQueueBytes > peakBytes {
			peakBytes = metrics.PeakQueueBytes
		}
		if metrics.MaxBatchEvents > maxBatchEvents {
			maxBatchEvents = metrics.MaxBatchEvents
		}
		if metrics.MaxBatchBytes > maxBatchBytes {
			maxBatchBytes = metrics.MaxBatchBytes
		}
		if metrics.MaxBatchLatency > maxBatchLatency {
			maxBatchLatency = metrics.MaxBatchLatency
		}
		if metrics.BatchLatencyP99 > maxBatchP99 {
			maxBatchP99 = metrics.BatchLatencyP99
		}
		if metrics.PeakQueueEvents > defaultPumpMaxEvents || metrics.PeakQueueBytes > defaultPumpMaxBytes {
			t.Fatalf("task %d queue peak = %d events/%d bytes", taskIndex, metrics.PeakQueueEvents, metrics.PeakQueueBytes)
		}
		if metrics.DroppedEvents != 0 || metrics.DroppedBytes != 0 {
			t.Fatalf("task %d dropped metrics = %#v", taskIndex, metrics)
		}
		if metrics.MaxBatchEvents > defaultWriterBatchEvents || metrics.MaxBatchBytes > defaultWriterBatchBytes {
			t.Fatalf("task %d batch bounds = %#v", taskIndex, metrics)
		}
		if !metrics.CheckpointComplete || metrics.CheckpointAttempts != 1 {
			t.Fatalf("task %d checkpoint metrics = %#v", taskIndex, metrics)
		}
		batches, checkpoints := harness.sink.Snapshot()
		if checkpoints != 1 {
			t.Fatalf("task %d checkpoints = %d, want 1", taskIndex, checkpoints)
		}
		var terminalCount int
		provider := fmt.Sprintf("provider-%d", taskIndex)
		for _, batch := range batches {
			for _, event := range batch {
				if event.Provider != provider {
					t.Fatalf("task %d persisted provider = %q, want %q", taskIndex, event.Provider, provider)
				}
				if event.Terminal {
					terminalCount++
				}
			}
		}
		if terminalCount != 1 {
			t.Fatalf("task %d terminal count = %d, want 1", taskIndex, terminalCount)
		}
	}
	if accepted != tasks*eventsPerTask {
		t.Fatalf("accepted events = %d, want %d", accepted, tasks*eventsPerTask)
	}
	t.Logf("events=%d peak_queue_events=%d peak_queue_bytes=%d max_batch_events=%d max_batch_bytes=%d max_batch_latency=%s batch_latency_p99_bucket=%s",
		accepted, peakEvents, peakBytes, maxBatchEvents, maxBatchBytes, maxBatchLatency, maxBatchP99)
}

func TestEventWriter_NonTransientFailureDoesNotRetry(t *testing.T) {
	sink := &t019RecordingSink{err: errors.New("permission denied")}
	writer, err := NewEventWriter(DefaultEventWriterConfig(sink))
	if err != nil {
		t.Fatalf("NewEventWriter: %v", err)
	}
	writer.Admit(testDelta("process.stdout", "x"))
	if err := writer.CloseAndFlush(context.Background()); !errors.Is(err, ErrArtifactSinkUnavailable) {
		t.Fatalf("CloseAndFlush error = %v", err)
	}
	if got := writer.Metrics().RetryAttempts; got != 1 {
		t.Fatalf("attempts = %d, want 1 for non-transient error", got)
	}
}

func TestEventWriter_SemanticQuotaExhaustionFailsInsteadOfReportingSuccess(t *testing.T) {
	sink := &t019BlockingSink{entered: make(chan struct{}), release: make(chan struct{})}
	config := DefaultEventWriterConfig(sink)
	config.Pump = eventPumpConfig{MaxEvents: 2, MaxBytes: 1024, ControlReserveEvents: 1, ControlReserveBytes: 256}
	config.FlushWindow = time.Nanosecond
	writer, err := NewEventWriter(config)
	if err != nil {
		t.Fatalf("NewEventWriter: %v", err)
	}
	first := RuntimeEvent{Provider: "codex", Channel: "tool", Type: "tool_result", Payload: map[string]any{"item": "first"}}
	second := RuntimeEvent{Provider: "codex", Channel: "tool", Type: "tool_result", Payload: map[string]any{"item": "second"}}
	third := RuntimeEvent{Provider: "codex", Channel: "tool", Type: "tool_result", Payload: map[string]any{"item": "third"}}
	if result := writer.Admit(first); result.Status != admissionAdmitted {
		t.Fatalf("first semantic admission = %#v", result)
	}
	select {
	case <-sink.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("writer did not enter blocked sink")
	}
	if result := writer.Admit(second); result.Status != admissionAdmitted {
		t.Fatalf("second semantic admission = %#v", result)
	}
	if result := writer.Admit(third); result.Status != admissionRejectedQuota {
		t.Fatalf("third semantic admission = %#v, want quota rejection", result)
	}
	close(sink.release)
	if err := writer.CloseAndFlush(context.Background()); !errors.Is(err, ErrArtifactSinkUnavailable) {
		t.Fatalf("CloseAndFlush error = %v, want ErrArtifactSinkUnavailable", err)
	}
}

func TestEventWriter_RejectedInvalidDurableEventsFailInsteadOfReportingSuccess(t *testing.T) {
	tests := []struct {
		name  string
		event RuntimeEvent
	}{
		{
			name: "semantic",
			event: RuntimeEvent{
				Provider: "codex",
				Channel:  "tool",
				Type:     "tool_result",
				Payload:  map[string]any{"result": func() {}},
			},
		},
		{
			name: "status",
			event: RuntimeEvent{
				Provider: "codex",
				Channel:  "system",
				Type:     "status",
				Payload:  map[string]any{"status": func() {}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			writer, err := NewEventWriter(DefaultEventWriterConfig(&t019RecordingSink{}))
			if err != nil {
				t.Fatalf("NewEventWriter: %v", err)
			}
			if result := writer.Admit(tt.event); result.Status != admissionRejectedInvalid {
				t.Fatalf("Admit = %#v, want rejected_invalid", result)
			}
			if err := writer.CloseAndFlush(context.Background()); !errors.Is(err, ErrArtifactSinkUnavailable) {
				t.Fatalf("CloseAndFlush error = %v, want ErrArtifactSinkUnavailable", err)
			}
		})
	}
}

func TestEventWriter_ValidStatusEventStillFlushes(t *testing.T) {
	writer, err := NewEventWriter(DefaultEventWriterConfig(&t019RecordingSink{}))
	if err != nil {
		t.Fatalf("NewEventWriter: %v", err)
	}
	result := writer.Admit(RuntimeEvent{
		Provider: "codex",
		Channel:  "system",
		Type:     "status",
		Payload:  map[string]any{"status": "running"},
	})
	if result.Status != admissionAdmitted {
		t.Fatalf("Admit = %#v, want admitted", result)
	}
	if err := writer.CloseAndFlush(context.Background()); err != nil {
		t.Fatalf("CloseAndFlush: %v", err)
	}
}

type t019BlockingSink struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (sink *t019BlockingSink) AppendRuntimeEvents(ctx context.Context, _ []RuntimeEvent) error {
	sink.once.Do(func() { close(sink.entered) })
	select {
	case <-sink.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (sink *t019BlockingSink) Checkpoint(context.Context) error { return nil }

func TestEventWriter_AdmissionRemainsResponsiveWhileSinkIsBlocked(t *testing.T) {
	sink := &t019BlockingSink{entered: make(chan struct{}), release: make(chan struct{})}
	config := DefaultEventWriterConfig(sink)
	config.FlushWindow = time.Nanosecond
	writer, err := NewEventWriter(config)
	if err != nil {
		t.Fatalf("NewEventWriter: %v", err)
	}
	writer.Admit(testDelta("process.stdout", "first"))
	select {
	case <-sink.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("writer did not enter the injected blocked sink")
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 1_000; i++ {
			result := writer.Admit(testDelta("process.stdout", "x"))
			if result.Status != admissionAdmitted && result.Status != admissionCoalesced {
				return
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("admission blocked behind the persistence call")
	}
	close(sink.release)
	if err := writer.CloseAndFlush(context.Background()); err != nil {
		t.Fatalf("CloseAndFlush: %v", err)
	}
}
