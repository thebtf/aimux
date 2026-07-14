package swarm

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/thebtf/aimux/pkg/types"
)

var (
	ErrExecutionNotFound      = errors.New("swarm: execution not found")
	ErrExecutionActive        = errors.New("swarm: handle already has an active execution")
	ErrExecutionExists        = errors.New("swarm: execution already exists")
	ErrEventAdmissionRejected = errors.New("swarm: terminal event admission rejected")
)

const maxTerminalExecutions = 256

// cancellationResolutionTimeout bounds cancellation resolution for optional
// native cancellers, including implementations that ignore their context.
// Tests shorten it through this package-private seam.
var cancellationResolutionTimeout = 5 * time.Second

// processEvidenceCaptureTimeout bounds a private exact-evidence lease. Tests
// shorten it through this package-private seam. Generic inspection providers
// are never used as terminal-finality sources.
var processEvidenceCaptureTimeout = cancellationResolutionTimeout

// beforeProcessEvidenceDeadlineFinalRead is a package-private deterministic
// test seam for the timer-selected boundary. Production leaves it nil.
var beforeProcessEvidenceDeadlineFinalRead func()

// beforeNativeCancellationResultSend and
// beforeNativeCancellationDeadlineFinalRead are package-private deterministic
// seams for the timeout-boundary final read. Production leaves them nil.
var (
	beforeNativeCancellationResultSend        func()
	beforeNativeCancellationDeadlineFinalRead func()
)

// beforeOwnedLeaseExecution is a package-private deterministic test seam after
// acquisition and before the only lease-consuming execution call.
var beforeOwnedLeaseExecution func()

// ExecutionInspection is an in-memory, fenced snapshot. It intentionally
// carries no durable binding; CR-003 owns recovery and persistence.
type ExecutionInspection struct {
	ExecutionID          types.ExecutionID
	Terminal             bool
	Cancelled            bool
	CancellationEvidence types.CancellationEvidence
	ProcessTreeEvidence  types.ProcessTreeEvidence
}

// SuppliedEvidenceClassification reports only what caller-supplied process
// evidence proves. Durable candidate discovery and restart recovery belong to
// CR-003.
type SuppliedEvidenceClassification string

const (
	SuppliedEvidenceUnknown       SuppliedEvidenceClassification = "unknown"
	SuppliedEvidenceLive          SuppliedEvidenceClassification = "live"
	SuppliedEvidenceCleanExit     SuppliedEvidenceClassification = "clean_exit"
	SuppliedEvidenceFailedCrash   SuppliedEvidenceClassification = "failed_crash"
	SuppliedEvidenceStaleIdentity SuppliedEvidenceClassification = "stale_identity"
	SuppliedEvidenceOrphanedTree  SuppliedEvidenceClassification = "orphaned_tree"
)

// SuppliedProcessEvidence contains explicit observations for one expected OS
// process generation. Pointer fields are optional evidence, never discovery
// requests. DescendantsSurvived is a positive observation, not an inference
// from ProcessTreeEvidence.Stopped=false.
type SuppliedProcessEvidence struct {
	ExpectedProcess     types.ProcessIdentity
	ObservedProcess     *types.ProcessIdentity
	RootAbsent          bool
	ExitCode            *int
	DescendantsSurvived bool
}

// SuppliedEvidenceInspection is immutable-by-value and contains no input
// pointers or live Swarm state.
type SuppliedEvidenceInspection struct {
	Classification SuppliedEvidenceClassification
}

type handleAuthority struct {
	tenantID   string
	scope      string
	generation uint64
	mode       SpawnMode
}

type executionKey struct {
	handle     *Handle
	generation uint64
	id         types.ExecutionID
}

type executionRecord struct {
	handle                     *Handle
	id                         types.ExecutionID
	tenantID                   string
	scope                      string
	generation                 uint64
	executor                   types.ExecutorV2
	mode                       SpawnMode
	sink                       types.ExecutorEventSink
	cancel                     context.CancelFunc
	cancellation               types.CancellationEvidence
	processEvidence            types.ProcessTreeEvidence
	hasProcessEvidence         bool
	executionDone              chan struct{}
	executionReturned          bool
	outcome                    executionOutcome
	exactProcessEvidence       bool
	cancelDone                 chan struct{}
	terminalDone               chan struct{}
	cancellationErr            error
	preSpawnCancelled          bool
	terminal                   bool
	cancelled                  bool
	truncated                  bool
	admitted                   bool
	mu                         sync.Mutex
	nativeCancellationInFlight bool
	operationLeaseTransferred  bool
}

type executionOutcome uint8

const (
	executionOutcomeUnknown executionOutcome = iota
	executionOutcomeCompleted
	executionOutcomeFailed
	executionOutcomeCancelled
)

// processEvidenceLease is private plumbing between Swarm and the concrete pipe
// adapter. The lease keeps the exact final snapshot available until Swarm has
// copied it into its immutable execution record; it is not a public capability.
type processEvidenceLease interface {
	// HoldProcessEvidence reports whether this runtime acquired the stateless
	// exact-final handoff. Inspection capability alone is not enough.
	HoldProcessEvidence(types.ExecutionID) bool
	ProcessEvidenceReady(types.ExecutionID) <-chan types.ProcessTreeEvidence
	ReleaseProcessEvidence(types.ExecutionID)
}

// ownedProcessEvidenceLease is private plumbing for the concrete stateless
// pipe path. The opaque value binds acquisition, one execution, and release.
type ownedProcessEvidenceLease interface {
	AcquireProcessEvidenceLease(types.ExecutionID) (any, <-chan types.ProcessTreeEvidence, bool)
	SendEventsWithProcessEvidenceLease(context.Context, types.ExecutionID, any, types.Message, types.ExecutorEventSink) (*types.Response, error)
	ReleaseProcessEvidenceLease(types.ExecutionID, any)
}

// Execute admits one exact execution for a live handle generation. Swarm owns
// the terminal winner; executors may propose output but cannot publish a
// terminal. Cancellation starts process shutdown, while Execute keeps the
// admission window open until the executor has drained and returned.
func (s *Swarm) Execute(ctx context.Context, h *Handle, scope string, id types.ExecutionID, msg types.Message, sink types.ExecutorEventSink) (*types.Response, error) {
	if err := id.Validate(); err != nil {
		return nil, err
	}
	if h == nil {
		return nil, ErrExecutionNotFound
	}
	if !h.tryAcquireOperation() {
		if !s.accepting() {
			return nil, ErrSwarmShutdown
		}
		return nil, ErrExecutionActive
	}
	operationLeaseOwned := true
	defer func() {
		if operationLeaseOwned {
			h.releaseOperation()
		}
	}()
	s.lifecycleMu.RLock()
	if s.shuttingDown {
		s.lifecycleMu.RUnlock()
		return nil, ErrSwarmShutdown
	}
	authority, exec, err := s.executionAuthority(ctx, h, scope)
	if err != nil {
		s.lifecycleMu.RUnlock()
		return nil, err
	}
	if sink == nil {
		sink = types.ExecutorEventSinkFunc(func(types.ExecutorEvent) bool { return true })
	}
	execCtx, cancel := context.WithCancel(ctx)
	record := &executionRecord{
		handle:        h,
		id:            id,
		tenantID:      authority.tenantID,
		scope:         authority.scope,
		generation:    authority.generation,
		executor:      exec,
		mode:          authority.mode,
		sink:          sink,
		cancel:        cancel,
		executionDone: make(chan struct{}),
		terminalDone:  make(chan struct{}),
	}
	key := executionKey{handle: h, generation: authority.generation, id: id}
	s.executionMu.Lock()
	if _, found := s.active[h]; found {
		s.executionMu.Unlock()
		s.lifecycleMu.RUnlock()
		cancel()
		return nil, ErrExecutionActive
	}
	if _, found := s.executions[key]; found {
		s.executionMu.Unlock()
		s.lifecycleMu.RUnlock()
		cancel()
		return nil, ErrExecutionExists
	}
	s.active[h] = record
	s.executions[key] = record
	s.executionMu.Unlock()
	s.lifecycleMu.RUnlock()
	defer cancel()

	h.mu.Lock()
	h.lastUsedAt = time.Now()
	h.mu.Unlock()

	admission := types.ExecutorEventSinkFunc(record.tryAdmit)
	var evidenceReady <-chan types.ProcessTreeEvidence
	var ownedLease ownedProcessEvidenceLease
	var lease any
	releaseOwnedLease := func() {
		if ownedLease != nil {
			ownedLease.ReleaseProcessEvidenceLease(id, lease)
			ownedLease = nil
		}
	}
	if holder, ok := exec.(ownedProcessEvidenceLease); ok {
		lease, evidenceReady, record.exactProcessEvidence = holder.AcquireProcessEvidenceLease(id)
		if record.exactProcessEvidence {
			ownedLease = holder
			defer releaseOwnedLease()
		}
	} else if holder, ok := exec.(processEvidenceLease); ok {
		record.exactProcessEvidence = holder.HoldProcessEvidence(id)
		if record.exactProcessEvidence {
			evidenceReady = holder.ProcessEvidenceReady(id)
			defer holder.ReleaseProcessEvidence(id)
		}
	}
	var response *types.Response
	if native, ok := exec.(types.EventExecutor); ok {
		if ownedLease != nil {
			if beforeOwnedLeaseExecution != nil {
				beforeOwnedLeaseExecution()
			}
			if err = execCtx.Err(); err != nil {
				releaseOwnedLease()
				record.markPreSpawnCancellation(err)
			} else {
				response, err = ownedLease.SendEventsWithProcessEvidenceLease(execCtx, id, lease, msg, admission)
			}
		} else {
			response, err = native.SendEvents(execCtx, id, msg, admission)
		}
	} else {
		response, err = exec.SendStream(execCtx, msg, func(chunk types.Chunk) {
			if !chunk.Done {
				admission.TryAdmit(types.ExecutorEvent{Channel: "stdout", Type: "text-only", Content: []byte(chunk.Content)})
			}
		})
	}
	providerContextErr := execCtx.Err()
	if ownedLease != nil && errors.Is(err, context.Canceled) && providerContextErr != nil {
		record.markPreSpawnCancellation(providerContextErr)
	}
	cancellationObservedAtReturn := providerContextErr != nil
	if s.beforeOutcomeCapture != nil {
		s.beforeOutcomeCapture()
	}
	record.markExecutionReturned(response, err, cancellationObservedAtReturn)
	record.captureProcessEvidence(evidenceReady)
	if s.postExecutorReturn != nil {
		s.postExecutorReturn()
	}
	if record.wasTruncated() {
		if response == nil {
			response = &types.Response{}
		}
		response.Partial = true
	}
	terminalAdmitted := record.finalizeTerminal(response, err)
	if !terminalAdmitted {
		if response == nil {
			response = &types.Response{}
		}
		response.Partial = true
	}
	s.complete(record, key)
	h.mu.Lock()
	h.lastUsedAt = time.Now()
	h.mu.Unlock()
	if record.transferOperationLeaseToNativeCancellation() {
		operationLeaseOwned = false
	} else if authority.mode == Stateless {
		_ = s.closeHandleLocked(h, "stateless-after-execution")
	}
	if !terminalAdmitted && err == nil {
		err = ErrEventAdmissionRejected
	}
	return response, err
}

// Cancel affects only the exact execution, scope, and current handle
// generation. Context cancellation is the common process-control path; native
// acknowledgement is additional evidence.
func (s *Swarm) Cancel(ctx context.Context, h *Handle, scope string, id types.ExecutionID, reason string) (types.CancellationEvidence, error) {
	record, err := s.execution(ctx, h, scope, id)
	if err != nil {
		return types.CancellationEvidence{}, err
	}
	return s.cancelRecord(ctx, record, reason)
}

func (s *Swarm) cancelRecord(ctx context.Context, record *executionRecord, reason string) (types.CancellationEvidence, error) {
	record.mu.Lock()
	if record.terminal {
		evidence := record.cancellation
		if evidence.ExecutionID == "" {
			evidence.ExecutionID = record.id
		}
		cancelErr := record.cancellationErr
		record.mu.Unlock()
		return evidence, cancelErr
	}
	if record.executionReturned {
		done := record.terminalDone
		record.mu.Unlock()
		return record.waitTerminal(ctx, done)
	}
	if done := record.cancelDone; done != nil {
		record.mu.Unlock()
		return record.waitCancellation(ctx, done)
	}
	record.cancellation = types.CancellationEvidence{ExecutionID: record.id}
	record.cancelDone = make(chan struct{})
	done := record.cancelDone
	record.mu.Unlock()
	go s.resolveCancellation(record, reason)
	return record.waitCancellation(ctx, done)
}

func (record *executionRecord) waitTerminal(ctx context.Context, done <-chan struct{}) (types.CancellationEvidence, error) {
	select {
	case <-done:
		record.mu.Lock()
		evidence, cancelErr := record.cancellation, record.cancellationErr
		if evidence.ExecutionID == "" {
			evidence.ExecutionID = record.id
		}
		record.mu.Unlock()
		return evidence, cancelErr
	case <-ctx.Done():
		return types.CancellationEvidence{ExecutionID: record.id}, ctx.Err()
	}
}

func (record *executionRecord) waitCancellation(ctx context.Context, done <-chan struct{}) (types.CancellationEvidence, error) {
	select {
	case <-done:
		record.mu.Lock()
		evidence, cancelErr := record.cancellation, record.cancellationErr
		record.mu.Unlock()
		return evidence, cancelErr
	case <-ctx.Done():
		return types.CancellationEvidence{ExecutionID: record.id}, ctx.Err()
	}
}

func (s *Swarm) resolveCancellation(record *executionRecord, reason string) {
	if record.cancel != nil {
		record.cancel()
	}
	ctx, cancel := context.WithTimeout(context.Background(), cancellationResolutionTimeout)
	defer cancel()
	evidence := types.CancellationEvidence{ExecutionID: record.id}
	var cancelErr error
	if canceller, ok := record.executor.(types.ExecutionCanceller); ok && s.tryAcquireNativeCancellation() {
		record.markNativeCancellationStarted()
		type nativeResult struct {
			evidence   types.CancellationEvidence
			err        error
			returnedAt time.Time
		}
		result := make(chan nativeResult, 1)
		go func() {
			defer s.releaseNativeCancellation()
			nativeEvidence, err := canceller.CancelExecution(ctx, record.id, reason)
			returnedAt := time.Now()
			if beforeNativeCancellationResultSend != nil {
				beforeNativeCancellationResultSend()
			}
			result <- nativeResult{evidence: nativeEvidence, err: err, returnedAt: returnedAt}
			s.finishNativeCancellation(record)
		}()
		deadline, _ := ctx.Deadline()
		accept := func(native nativeResult) {
			if native.returnedAt.After(deadline) {
				cancelErr = context.DeadlineExceeded
				return
			}
			evidence, cancelErr = native.evidence, native.err
			if evidence.ExecutionID == "" {
				evidence.ExecutionID = record.id
			} else if evidence.ExecutionID != record.id {
				evidence = types.CancellationEvidence{ExecutionID: record.id}
				cancelErr = types.NewValidationError("native cancellation execution ID must match requested execution ID")
			}
		}
		select {
		case native := <-result:
			accept(native)
		case <-ctx.Done():
			if beforeNativeCancellationDeadlineFinalRead != nil {
				beforeNativeCancellationDeadlineFinalRead()
			}
			select {
			case native := <-result:
				accept(native)
			default:
				cancelErr = ctx.Err()
			}
		}
	}
	if _, ok := record.executor.(types.ProcessEvidenceProvider); ok {
		select {
		case <-record.executionDone:
		case <-ctx.Done():
		}
	}
	record.mu.Lock()
	record.cancellation = evidence
	record.cancellationErr = cancelErr
	close(record.cancelDone)
	record.mu.Unlock()
}

func (record *executionRecord) markNativeCancellationStarted() {
	record.mu.Lock()
	record.nativeCancellationInFlight = true
	record.mu.Unlock()
}

// transferOperationLeaseToNativeCancellation atomically chooses who releases
// Execute's already-held handle operation lease when provider return races the
// bounded terminal path.
func (record *executionRecord) transferOperationLeaseToNativeCancellation() bool {
	record.mu.Lock()
	defer record.mu.Unlock()
	if !record.nativeCancellationInFlight {
		return false
	}
	record.operationLeaseTransferred = true
	return true
}

// finishNativeCancellation records actual provider return and consumes a
// transferred handle operation lease, if Execute handed it off while the call
// was still in flight.
func (s *Swarm) finishNativeCancellation(record *executionRecord) {
	record.mu.Lock()
	record.nativeCancellationInFlight = false
	transferred := record.operationLeaseTransferred
	record.operationLeaseTransferred = false
	record.mu.Unlock()
	if !transferred {
		return
	}
	if record.mode == Stateless {
		_ = s.closeHandleLocked(record.handle, "stateless-after-execution")
	}
	record.handle.releaseOperation()
}

func (s *Swarm) tryAcquireNativeCancellation() bool {
	select {
	case s.nativeCancellationGate <- struct{}{}:
		return true
	default:
		return false
	}
}

func (s *Swarm) releaseNativeCancellation() {
	<-s.nativeCancellationGate
}

// InspectSuppliedEvidence classifies explicit process evidence without reading
// the Swarm registry, constructing an executor, or spawning/resuming work.
func (*Swarm) InspectSuppliedEvidence(evidence *SuppliedProcessEvidence) SuppliedEvidenceInspection {
	return classifySuppliedEvidence(evidence)
}

func classifySuppliedEvidence(evidence *SuppliedProcessEvidence) SuppliedEvidenceInspection {
	inspection := SuppliedEvidenceInspection{Classification: SuppliedEvidenceUnknown}
	if evidence == nil || evidence.ExpectedProcess.Validate() != nil {
		return inspection
	}
	if evidence.ExitCode != nil && *evidence.ExitCode < 0 {
		return inspection
	}

	if observed := evidence.ObservedProcess; observed != nil {
		if observed.Validate() != nil || observed.PID != evidence.ExpectedProcess.PID {
			return inspection
		}
		if observed.StartFingerprint != evidence.ExpectedProcess.StartFingerprint {
			if evidence.DescendantsSurvived {
				inspection.Classification = SuppliedEvidenceOrphanedTree
			} else {
				inspection.Classification = SuppliedEvidenceStaleIdentity
			}
			return inspection
		}
		if *observed != evidence.ExpectedProcess || evidence.RootAbsent || evidence.ExitCode != nil || evidence.DescendantsSurvived {
			return inspection
		}
		inspection.Classification = SuppliedEvidenceLive
		return inspection
	}

	if evidence.DescendantsSurvived {
		if evidence.RootAbsent || evidence.ExitCode != nil {
			inspection.Classification = SuppliedEvidenceOrphanedTree
		}
		return inspection
	}
	if evidence.ExitCode == nil {
		return inspection
	}
	if *evidence.ExitCode == 0 {
		inspection.Classification = SuppliedEvidenceCleanExit
	} else {
		inspection.Classification = SuppliedEvidenceFailedCrash
	}
	return inspection
}

// Inspect returns the bounded, fenced in-memory execution snapshot without
// spawning or enumerating durable state.
func (s *Swarm) Inspect(ctx context.Context, h *Handle, scope string, id types.ExecutionID) (ExecutionInspection, error) {
	record, err := s.execution(ctx, h, scope, id)
	if err != nil {
		return ExecutionInspection{}, err
	}
	record.mu.Lock()
	waitTerminal := record.executionReturned && !record.terminal || record.cancelDone != nil
	terminalDone := record.terminalDone
	record.mu.Unlock()
	if waitTerminal {
		select {
		case <-terminalDone:
		case <-ctx.Done():
			return ExecutionInspection{}, ctx.Err()
		}
	}
	record.mu.Lock()
	defer record.mu.Unlock()
	return ExecutionInspection{ExecutionID: id, Terminal: record.terminal, Cancelled: record.cancelled, CancellationEvidence: record.cancellation, ProcessTreeEvidence: record.processEvidence}, nil
}

func (s *Swarm) executionAuthority(ctx context.Context, h *Handle, scope string) (handleAuthority, types.ExecutorV2, error) {
	if h == nil {
		return handleAuthority{}, nil, ErrExecutionNotFound
	}
	s.mu.RLock()
	authority, found := s.live[h]
	s.mu.RUnlock()
	if !found || authority.tenantID != tenantIDFromContext(ctx) || authority.scope != scope {
		return handleAuthority{}, nil, ErrExecutionNotFound
	}
	h.mu.Lock()
	exec := h.executor
	generation := h.generation
	h.mu.Unlock()
	if exec == nil || generation != authority.generation {
		return handleAuthority{}, nil, ErrExecutionNotFound
	}
	return authority, exec, nil
}

func (s *Swarm) execution(ctx context.Context, h *Handle, scope string, id types.ExecutionID) (*executionRecord, error) {
	if h == nil {
		return nil, ErrExecutionNotFound
	}
	h.mu.Lock()
	generation := h.generation
	h.mu.Unlock()
	key := executionKey{handle: h, generation: generation, id: id}
	s.executionMu.Lock()
	record := s.executions[key]
	s.executionMu.Unlock()
	if record == nil || record.tenantID != tenantIDFromContext(ctx) || record.scope != scope {
		return nil, ErrExecutionNotFound
	}
	return record, nil
}

func (record *executionRecord) tryAdmit(event types.ExecutorEvent) bool {
	record.mu.Lock()
	defer record.mu.Unlock()
	if record.terminal {
		return false
	}
	if event.Terminal {
		return true
	}
	accepted := record.sink.TryAdmit(event)
	if !accepted {
		record.truncated = true
	}
	return accepted
}

func (record *executionRecord) finalizeTerminal(response *types.Response, runErr error) bool {
	record.mu.Lock()
	done := record.cancelDone
	record.mu.Unlock()
	if done != nil {
		<-done
	}
	record.mu.Lock()
	defer record.mu.Unlock()
	if record.terminal {
		return record.admitted
	}
	if response != nil && response.Partial {
		record.truncated = true
	}
	eventType := "completed"
	processCancellationProven := record.hasProcessEvidence && record.processEvidence.Stopped && record.processEvidence.Validate() == nil
	cancellationProven := record.cancellation.NativeAcknowledged || processCancellationProven || record.preSpawnCancelled
	if record.outcome == executionOutcomeCancelled && cancellationProven {
		eventType = "cancelled"
	} else if response != nil && response.Error != nil && response.Error.Type == types.ErrorTypeTimeout {
		eventType = "timeout"
	} else if record.outcome != executionOutcomeCompleted || record.exactProcessEvidence && (!record.hasProcessEvidence || !record.processEvidence.Stopped) {
		eventType = "failed"
	}
	record.terminal = true
	record.cancelled = eventType == "cancelled"
	record.admitted = record.sink.TryAdmit(types.ExecutorEvent{
		Channel:   "terminal",
		Type:      eventType,
		Terminal:  true,
		Truncated: record.truncated,
	})
	if !record.admitted {
		record.truncated = true
	}
	close(record.terminalDone)
	return record.admitted
}

func (record *executionRecord) markExecutionReturned(response *types.Response, runErr error, cancellationObservedAtReturn bool) {
	record.mu.Lock()
	if !record.executionReturned {
		record.outcome = classifyExecutionOutcome(response, runErr, cancellationObservedAtReturn)
		record.executionReturned = true
		close(record.executionDone)
	}
	record.mu.Unlock()
}

func (record *executionRecord) markPreSpawnCancellation(err error) {
	record.mu.Lock()
	defer record.mu.Unlock()
	record.preSpawnCancelled = true
	if record.cancelDone != nil {
		return
	}
	record.cancellation = types.CancellationEvidence{ExecutionID: record.id}
	record.cancellationErr = err
	record.cancelDone = make(chan struct{})
	close(record.cancelDone)
}

func (record *executionRecord) captureProcessEvidence(ready <-chan types.ProcessTreeEvidence) {
	if !record.exactProcessEvidence || ready == nil {
		return
	}
	accept := func(evidence types.ProcessTreeEvidence, ok bool) {
		if !ok || evidence.Validate() != nil {
			return
		}
		record.mu.Lock()
		record.processEvidence = evidence
		record.hasProcessEvidence = true
		record.mu.Unlock()
	}
	// Prefer a value already available at the deadline boundary. Go select makes
	// two ready cases pseudo-random, so timeout gets a final non-blocking read.
	select {
	case evidence, ok := <-ready:
		accept(evidence, ok)
		return
	default:
	}
	timer := time.NewTimer(processEvidenceCaptureTimeout)
	defer timer.Stop()
	select {
	case evidence, ok := <-ready:
		accept(evidence, ok)
	case <-timer.C:
		if beforeProcessEvidenceDeadlineFinalRead != nil {
			beforeProcessEvidenceDeadlineFinalRead()
		}
		select {
		case evidence, ok := <-ready:
			accept(evidence, ok)
		default:
		}
	}
}

func classifyExecutionOutcome(response *types.Response, runErr error, cancellationRequested bool) executionOutcome {
	if cancellationRequested && (errors.Is(runErr, context.Canceled) || response != nil && response.Error != nil && errors.Is(response.Error, context.Canceled)) {
		return executionOutcomeCancelled
	}
	if executionSucceeded(response, runErr) {
		return executionOutcomeCompleted
	}
	return executionOutcomeFailed
}

func executionSucceeded(response *types.Response, runErr error) bool {
	return runErr == nil && (response == nil || response.Error == nil && response.ExitCode == 0)
}

func (record *executionRecord) wasTruncated() bool {
	record.mu.Lock()
	defer record.mu.Unlock()
	return record.truncated
}

func (s *Swarm) complete(record *executionRecord, key executionKey) {
	s.executionMu.Lock()
	if activeRecord, ok := s.active[record.handle]; ok && activeRecord == record {
		delete(s.active, record.handle)
	}
	s.terminalOrder = append(s.terminalOrder, key)
	for len(s.terminalOrder) > maxTerminalExecutions {
		oldest := s.terminalOrder[0]
		s.terminalOrder = s.terminalOrder[1:]
		delete(s.executions, oldest)
	}
	s.executionMu.Unlock()
}
