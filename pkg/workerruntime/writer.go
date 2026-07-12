package workerruntime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

const (
	defaultWriterBatchEvents  = 256
	defaultWriterBatchBytes   = 4 << 20
	defaultWriterFlushWindow  = 25 * time.Millisecond
	defaultWriterMaxAttempts  = 8
	defaultWriterRetryBudget  = 5 * time.Second
	defaultWriterInitialDelay = 10 * time.Millisecond
	defaultWriterMaxDelay     = time.Second
	defaultNormalizerFrame    = 1 << 20
	defaultNormalizerPayload  = 64 << 10
)

var eventWriterLatencyBuckets = [...]time.Duration{
	time.Millisecond,
	5 * time.Millisecond,
	10 * time.Millisecond,
	25 * time.Millisecond,
	50 * time.Millisecond,
	100 * time.Millisecond,
	250 * time.Millisecond,
	500 * time.Millisecond,
	time.Second,
	5 * time.Second,
	10 * time.Second,
}

var ErrArtifactSinkUnavailable = errors.New("artifact_sink_unavailable")

type EventBatchSink interface {
	AppendRuntimeEvents(context.Context, []RuntimeEvent) error
	Checkpoint(context.Context) error
}

type EventWriterTimer interface {
	C() <-chan time.Time
	Stop() bool
}

type EventWriterConfig struct {
	Sink           EventBatchSink
	Pump           eventPumpConfig
	BatchMaxEvents int
	BatchMaxBytes  int
	FlushWindow    time.Duration
	MaxAttempts    int
	RetryBudget    time.Duration
	Now            func() time.Time
	Wait           func(context.Context, time.Duration) error
	NewTimer       func(time.Duration) EventWriterTimer
	Backoff        func(int) time.Duration
	IsTransient    func(error) bool
}

type EventWriterMetrics struct {
	PeakQueueEvents    int           `json:"peak_queue_events"`
	PeakQueueBytes     int           `json:"peak_queue_bytes"`
	AdmittedEvents     uint64        `json:"admitted_events"`
	AdmittedBytes      uint64        `json:"admitted_bytes"`
	CoalescedEvents    uint64        `json:"coalesced_events"`
	CoalescedBytes     uint64        `json:"coalesced_bytes"`
	DroppedEvents      uint64        `json:"dropped_events"`
	DroppedBytes       uint64        `json:"dropped_bytes"`
	RetryAttempts      uint64        `json:"retry_attempts"`
	WriterFailures     uint64        `json:"writer_failures"`
	PersistedBatches   uint64        `json:"persisted_batches"`
	PersistedEvents    uint64        `json:"persisted_events"`
	MaxBatchEvents     int           `json:"max_batch_events"`
	MaxBatchBytes      int           `json:"max_batch_bytes"`
	MaxBatchLatency    time.Duration `json:"max_batch_latency"`
	LastBatchEvents    int           `json:"last_batch_events"`
	LastBatchBytes     int           `json:"last_batch_bytes"`
	LastBatchLatency   time.Duration `json:"last_batch_latency"`
	BatchLatencyP99    time.Duration `json:"batch_latency_p99"`
	CheckpointAttempts uint64        `json:"checkpoint_attempts"`
	CheckpointFailures uint64        `json:"checkpoint_failures"`
	CheckpointComplete bool          `json:"checkpoint_complete"`
}

type EventWriter struct {
	config EventWriterConfig
	pump   *eventPump

	wake        chan struct{}
	inputClosed chan struct{}
	done        chan struct{}
	failure     chan error
	runCtx      context.Context
	cancelRun   context.CancelFunc
	closeOnce   sync.Once
	failOnce    sync.Once

	normalizersMu sync.Mutex
	normalizers   map[string]*eventNormalizer

	metricsMu      sync.Mutex
	metrics        EventWriterMetrics
	latencyBuckets [len(eventWriterLatencyBuckets)]uint64
	latencyCount   uint64
	errorMu        sync.Mutex
	err            error
}

type systemEventWriterTimer struct {
	timer *time.Timer
}

func (timer systemEventWriterTimer) C() <-chan time.Time { return timer.timer.C }
func (timer systemEventWriterTimer) Stop() bool          { return timer.timer.Stop() }

type artifactSinkError struct {
	cause error
}

func (err *artifactSinkError) Error() string { return ErrArtifactSinkUnavailable.Error() }
func (err *artifactSinkError) Unwrap() error { return ErrArtifactSinkUnavailable }
func (err *artifactSinkError) Cause() error {
	if err == nil {
		return nil
	}
	return err.cause
}

func DefaultEventWriterConfig(sink EventBatchSink) EventWriterConfig {
	return EventWriterConfig{
		Sink:           sink,
		Pump:           defaultEventPumpConfig(),
		BatchMaxEvents: defaultWriterBatchEvents,
		BatchMaxBytes:  defaultWriterBatchBytes,
		FlushWindow:    defaultWriterFlushWindow,
		MaxAttempts:    defaultWriterMaxAttempts,
		RetryBudget:    defaultWriterRetryBudget,
		Now:            time.Now,
		Wait:           waitEventWriter,
		NewTimer: func(delay time.Duration) EventWriterTimer {
			return systemEventWriterTimer{timer: time.NewTimer(delay)}
		},
		Backoff:     eventWriterBackoff,
		IsTransient: IsTransientSQLiteError,
	}
}

func NewEventWriter(config EventWriterConfig) (*EventWriter, error) {
	if config.Sink == nil {
		return nil, errors.New("event writer sink is required")
	}
	if config.Pump.MaxEvents == 0 {
		config.Pump = defaultEventPumpConfig()
	}
	if config.BatchMaxEvents <= 0 || config.BatchMaxEvents > 1024 {
		return nil, errors.New("event writer batch event limit must be within 1..1024")
	}
	if config.BatchMaxBytes <= 0 || config.BatchMaxBytes > 16<<20 {
		return nil, errors.New("event writer batch byte limit must be within 1..16 MiB")
	}
	if config.FlushWindow <= 0 || config.FlushWindow > 100*time.Millisecond {
		return nil, errors.New("event writer flush window must be within 1ns..100ms")
	}
	if config.MaxAttempts <= 0 || config.MaxAttempts > defaultWriterMaxAttempts {
		return nil, fmt.Errorf("event writer max attempts must be within 1..%d", defaultWriterMaxAttempts)
	}
	if config.RetryBudget <= 0 || config.RetryBudget > defaultWriterRetryBudget {
		return nil, fmt.Errorf("event writer retry budget must be within 1ns..%s", defaultWriterRetryBudget)
	}
	if config.Now == nil || config.Wait == nil || config.NewTimer == nil || config.Backoff == nil || config.IsTransient == nil {
		return nil, errors.New("event writer deterministic time and classification seams are required")
	}
	ordinaryByteCapacity := config.Pump.MaxBytes - config.Pump.ControlReserveBytes
	coalesceLimit := config.BatchMaxBytes
	if ordinaryByteCapacity > 0 && ordinaryByteCapacity < coalesceLimit {
		coalesceLimit = ordinaryByteCapacity
	}
	if config.Pump.CoalesceMaxBytes > 0 && config.Pump.CoalesceMaxBytes < coalesceLimit {
		coalesceLimit = config.Pump.CoalesceMaxBytes
	}
	config.Pump.CoalesceMaxBytes = coalesceLimit
	pump, err := newEventPump(config.Pump)
	if err != nil {
		return nil, err
	}
	runCtx, cancelRun := context.WithCancel(context.Background())
	writer := &EventWriter{
		config:      config,
		pump:        pump,
		wake:        make(chan struct{}, 1),
		inputClosed: make(chan struct{}),
		done:        make(chan struct{}),
		failure:     make(chan error, 1),
		runCtx:      runCtx,
		cancelRun:   cancelRun,
		normalizers: make(map[string]*eventNormalizer),
	}
	go writer.run()
	return writer, nil
}

func (writer *EventWriter) Admit(event RuntimeEvent) AdmissionResult {
	if writer == nil || writer.pump == nil {
		return AdmissionResult{Status: admissionRejectedClosed}
	}
	result := writer.pump.admit(event)
	writer.recordAdmission(result)
	if (result.Status == admissionRejectedQuota || result.Status == admissionRejectedInvalid) && (isControlEvent(event) || isDurableSemanticEvent(event)) {
		writer.fail(fmt.Errorf("durable event admission %s", result.Status))
		writer.signal()
	}
	if result.Status == admissionAdmitted || result.Status == admissionCoalesced {
		writer.signal()
	}
	return result
}

func (writer *EventWriter) AdmitOutput(provider, format, line string) []AdmissionResult {
	if writer == nil || strings.TrimSpace(line) == "" {
		return nil
	}
	normalizer, err := writer.normalizer(provider, format)
	if err != nil {
		writer.fail(err)
		writer.signal()
		return []AdmissionResult{{Status: admissionRejectedInvalid}}
	}
	events, err := normalizer.feed("process.stdout", append([]byte(line), '\n'))
	if err != nil {
		writer.fail(err)
		writer.signal()
		return []AdmissionResult{{Status: admissionRejectedInvalid}}
	}
	results := make([]AdmissionResult, 0, len(events))
	for _, event := range events {
		results = append(results, writer.Admit(event))
	}
	return results
}

func (writer *EventWriter) Failure() <-chan error {
	if writer == nil {
		return nil
	}
	return writer.failure
}

func (writer *EventWriter) Err() error {
	if writer == nil {
		return nil
	}
	writer.errorMu.Lock()
	defer writer.errorMu.Unlock()
	return writer.err
}

func (writer *EventWriter) Metrics() EventWriterMetrics {
	if writer == nil {
		return EventWriterMetrics{}
	}
	writer.metricsMu.Lock()
	defer writer.metricsMu.Unlock()
	snapshot := writer.metrics
	if writer.latencyCount > 0 {
		target := (writer.latencyCount*99 + 99) / 100
		var cumulative uint64
		for index, count := range writer.latencyBuckets {
			cumulative += count
			if cumulative >= target {
				snapshot.BatchLatencyP99 = eventWriterLatencyBuckets[index]
				break
			}
		}
	}
	return snapshot
}

func (writer *EventWriter) CloseAndFlush(ctx context.Context) error {
	if writer == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	writer.closeOnce.Do(func() {
		writer.pump.close()
		close(writer.inputClosed)
		writer.signal()
	})
	select {
	case <-writer.done:
		return writer.Err()
	case <-ctx.Done():
		writer.cancelRun()
		<-writer.done
		if err := writer.Err(); err != nil {
			return err
		}
		return ctx.Err()
	}
}

func (writer *EventWriter) run() {
	defer close(writer.done)
	defer writer.cancelRun()
	inputClosed := false
	for {
		batchStarted := writer.config.Now()
		if !inputClosed {
			select {
			case <-writer.wake:
			case <-writer.inputClosed:
				inputClosed = true
			case <-writer.runCtx.Done():
				writer.fail(writer.runCtx.Err())
				return
			}
		}
		if !inputClosed {
			inputClosed = writer.gatherWindow()
		}
		batch := writer.pump.drainBounded(writer.config.BatchMaxEvents, writer.config.BatchMaxBytes)
		if len(batch) > 0 {
			if err := writer.retry("append", func(ctx context.Context) error {
				return writer.config.Sink.AppendRuntimeEvents(ctx, batch)
			}); err != nil {
				writer.fail(err)
				return
			}
			writer.recordBatch(batch, writer.config.Now().Sub(batchStarted))
			continue
		}
		queued, _, _ := writer.pump.stats()
		if queued > 0 {
			writer.fail(errors.New("event exceeds writer batch byte limit"))
			return
		}
		if inputClosed {
			writer.recordCheckpointAttempt()
			if err := writer.retry("checkpoint", writer.config.Sink.Checkpoint); err != nil {
				writer.recordCheckpointFailure()
				writer.fail(err)
				return
			}
			writer.recordCheckpointComplete()
			return
		}
	}
}

func (writer *EventWriter) gatherWindow() bool {
	timer := writer.config.NewTimer(writer.config.FlushWindow)
	defer timer.Stop()
	for {
		events, bytes, _ := writer.pump.stats()
		if events >= writer.config.BatchMaxEvents || bytes >= writer.config.BatchMaxBytes {
			return false
		}
		select {
		case <-writer.wake:
			continue
		case <-timer.C():
			return false
		case <-writer.inputClosed:
			return true
		case <-writer.runCtx.Done():
			return true
		}
	}
}

func (writer *EventWriter) retry(_ string, operation func(context.Context) error) error {
	started := writer.config.Now()
	var last error
	for attempt := 1; attempt <= writer.config.MaxAttempts; attempt++ {
		remaining := writer.config.RetryBudget - writer.config.Now().Sub(started)
		if remaining <= 0 {
			break
		}
		opCtx, cancel := context.WithTimeout(writer.runCtx, remaining)
		last = operation(opCtx)
		cancel()
		writer.recordRetryAttempt()
		if last == nil {
			return nil
		}
		if !writer.config.IsTransient(last) || attempt == writer.config.MaxAttempts {
			break
		}
		delay := writer.config.Backoff(attempt)
		if delay <= 0 || writer.config.Now().Sub(started)+delay > writer.config.RetryBudget {
			break
		}
		if err := writer.config.Wait(writer.runCtx, delay); err != nil {
			last = err
			break
		}
	}
	if last == nil {
		last = errors.New("event writer retry budget exhausted")
	}
	return last
}

func (writer *EventWriter) fail(cause error) {
	writer.failOnce.Do(func() {
		writer.pump.close()
		err := &artifactSinkError{cause: cause}
		writer.errorMu.Lock()
		writer.err = err
		writer.errorMu.Unlock()
		writer.metricsMu.Lock()
		writer.metrics.WriterFailures++
		writer.metricsMu.Unlock()
		writer.failure <- err
		close(writer.failure)
	})
}

func (writer *EventWriter) signal() {
	select {
	case writer.wake <- struct{}{}:
	default:
	}
}

func (writer *EventWriter) normalizer(provider, format string) (*eventNormalizer, error) {
	provider = strings.TrimSpace(provider)
	format = strings.ToLower(strings.TrimSpace(format))
	key := provider + "\x00" + format
	writer.normalizersMu.Lock()
	defer writer.normalizersMu.Unlock()
	if normalizer := writer.normalizers[key]; normalizer != nil {
		return normalizer, nil
	}
	normalizer, err := newEventNormalizer(eventNormalizerConfig{
		Provider:          provider,
		Format:            format,
		SchemaFingerprint: provider + "-runtime-v1",
		MaxFrameBytes:     defaultNormalizerFrame,
		MaxPayloadBytes:   defaultNormalizerPayload,
	})
	if err != nil {
		return nil, err
	}
	writer.normalizers[key] = normalizer
	return normalizer, nil
}

func (writer *EventWriter) recordAdmission(result AdmissionResult) {
	events, bytes := writer.pump.peaks()
	writer.metricsMu.Lock()
	defer writer.metricsMu.Unlock()
	writer.metrics.AdmittedEvents += result.AdmittedEvents
	writer.metrics.AdmittedBytes += result.AdmittedBytes
	writer.metrics.CoalescedEvents += result.CoalescedEvents
	writer.metrics.CoalescedBytes += result.CoalescedBytes
	writer.metrics.DroppedEvents += result.DroppedEvents
	writer.metrics.DroppedBytes += result.DroppedBytes
	if events > writer.metrics.PeakQueueEvents {
		writer.metrics.PeakQueueEvents = events
	}
	if bytes > writer.metrics.PeakQueueBytes {
		writer.metrics.PeakQueueBytes = bytes
	}
}

func (writer *EventWriter) recordRetryAttempt() {
	writer.metricsMu.Lock()
	writer.metrics.RetryAttempts++
	writer.metricsMu.Unlock()
}

func (writer *EventWriter) recordBatch(batch []RuntimeEvent, latency time.Duration) {
	bytes := 0
	for _, event := range batch {
		_, size, err := ownRuntimeEvent(event)
		if err == nil {
			bytes += size
		}
	}
	writer.metricsMu.Lock()
	defer writer.metricsMu.Unlock()
	writer.metrics.PersistedBatches++
	writer.metrics.PersistedEvents += uint64(len(batch))
	if len(batch) > writer.metrics.MaxBatchEvents {
		writer.metrics.MaxBatchEvents = len(batch)
	}
	if bytes > writer.metrics.MaxBatchBytes {
		writer.metrics.MaxBatchBytes = bytes
	}
	if latency > writer.metrics.MaxBatchLatency {
		writer.metrics.MaxBatchLatency = latency
	}
	writer.metrics.LastBatchEvents = len(batch)
	writer.metrics.LastBatchBytes = bytes
	writer.metrics.LastBatchLatency = latency
	writer.latencyCount++
	bucket := len(eventWriterLatencyBuckets) - 1
	for index, bound := range eventWriterLatencyBuckets {
		if latency <= bound {
			bucket = index
			break
		}
	}
	writer.latencyBuckets[bucket]++
}

func (writer *EventWriter) recordCheckpointAttempt() {
	writer.metricsMu.Lock()
	writer.metrics.CheckpointAttempts++
	writer.metricsMu.Unlock()
}

func (writer *EventWriter) recordCheckpointFailure() {
	writer.metricsMu.Lock()
	writer.metrics.CheckpointFailures++
	writer.metricsMu.Unlock()
}

func (writer *EventWriter) recordCheckpointComplete() {
	writer.metricsMu.Lock()
	writer.metrics.CheckpointComplete = true
	writer.metricsMu.Unlock()
}

func IsTransientSQLiteError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "sqlite_busy") ||
		strings.Contains(message, "sqlite_locked") ||
		strings.Contains(message, "database is locked") ||
		strings.Contains(message, "database table is locked")
}

func isDurableSemanticEvent(event RuntimeEvent) bool {
	if isControlEvent(event) {
		return true
	}
	switch event.Type {
	case "text_delta", "command_output_delta", "provider_event_unknown", "output_truncated":
		return false
	default:
		return true
	}
}

func eventWriterBackoff(attempt int) time.Duration {
	if attempt <= 0 {
		return 0
	}
	delay := defaultWriterInitialDelay << (attempt - 1)
	if delay > defaultWriterMaxDelay {
		return defaultWriterMaxDelay
	}
	return delay
}

func waitEventWriter(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
