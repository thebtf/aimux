package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/thebtf/aimux/loom"
	"github.com/thebtf/aimux/pkg/config"
	"github.com/thebtf/aimux/pkg/executor/picker"
	"github.com/thebtf/aimux/pkg/swarm"
	"github.com/thebtf/aimux/pkg/tenant"
	"github.com/thebtf/aimux/pkg/types"
	"github.com/thebtf/aimux/pkg/workerruntime"
)

// Default durable lease window for one Run Binding attempt and the cadence
// the coordinator renews it on. The cadence stays well under the TTL so a
// scheduling delay never lets the lease lapse before the next renewal lands.
// taskBindingCleanupTimeout bounds post-reservation cleanup (closing a live
// handle this attempt owns, finalizing/recording durable outcome) run on a
// context detached from the caller's own cancellation — the task/timeout
// context dispatch was called with may already be cancelled by the time
// cleanup runs, and that cleanup must still complete.
const (
	taskBindingLeaseTTL       = 2 * time.Minute
	taskBindingRenewalCadence = 30 * time.Second
	taskBindingCleanupTimeout = 15 * time.Second

	taskBindingReasonAcquireFailed         = "acquire_failed"
	taskBindingReasonLiveGenerationInvalid = "live_generation_invalid"
	taskBindingReasonStartFailed           = "start_failed"
	taskBindingReasonPreProviderRejected   = "pre_provider_rejected"
	taskBindingReasonPanicCleanup          = "panic_cleanup"
)

var errTaskBindingProviderNotAttempted = errors.New("task binding: provider was not attempted")

// TaskBindingIdentity carries the provider-neutral identity a caller already
// knows before dispatch: the durable Loom task plus tenant/project scoping
// and deterministic profile/capability fingerprints (CR-003). Callers
// populate it from the durable loom.Task record and the resolved CLI
// profile — never from public request input — so it never widens a public
// MCP schema. TenantID must be the exact durable task tenant (LoomEngine.Submit
// already normalizes an unset caller tenant to loom.LegacyTenantID before
// persisting the task, so a genuinely blank TenantID here means an upstream
// bug, not single-tenant mode); dispatch passes it through unchanged and
// fails closed rather than minting one. ProjectID may be genuinely blank for
// a stateless attempt (no-project callers) and is passed through unchanged
// to Loom, which durably stores that same blank value rather than a
// sentinel that would mismatch the owning task row; session-backed modes
// (new/exact_resume/fork) always require a nonblank project and Loom fails
// the reserve closed if one is missing.
type TaskBindingIdentity struct {
	TaskID                string
	TenantID              string
	ProjectID             string
	ProfileFingerprint    string
	CapabilityFingerprint string
}

// taskSessionRequest describes the requested Worker Session binding for one
// dispatch attempt. The zero value is stateless. CR-003's public task/review
// surface does not yet expose session modes (that lands in a later CR), so
// every production caller leaves this at its zero value; non-stateless
// modes are exercised directly against taskBindingCoordinator by tests.
type taskSessionRequest struct {
	Mode                  types.SessionBindingMode
	WorkerSessionID       string
	ParentWorkerSessionID string
	Expected              *types.SessionBindingIdentity
	Parent                *types.SessionBindingIdentity
}

// runtimeMode maps the requested session-binding mode to Loom's durable
// runtime-binding mode. Blank and the explicit stateless constant are the
// only values that may durably reserve as stateless; anything else unknown
// fails closed rather than silently downgrading to stateless.
func (r taskSessionRequest) runtimeMode() (loom.RuntimeBindingMode, error) {
	switch r.Mode {
	case "", types.SessionBindingModeStateless:
		return loom.RuntimeBindingModeStateless, nil
	case types.SessionBindingModeNew:
		return loom.RuntimeBindingModeNew, nil
	case types.SessionBindingModeExactResume:
		return loom.RuntimeBindingModeExactResume, nil
	case types.SessionBindingModeFork:
		return loom.RuntimeBindingModeFork, nil
	default:
		return "", fmt.Errorf("unknown session binding mode %q", r.Mode)
	}
}

func (r taskSessionRequest) bindingRequest() types.SessionBindingRequest {
	mode := r.Mode
	if mode == "" {
		mode = types.SessionBindingModeStateless
	}
	return types.SessionBindingRequest{Mode: mode, Expected: r.Expected, Parent: r.Parent}
}

// leaseRenewalTicker abstracts a periodic renewal signal so tests can drive
// exact ticks deterministically instead of racing wall-clock sleeps.
type leaseRenewalTicker interface {
	C() <-chan time.Time
	Stop()
}

type wallClockLeaseTicker struct{ ticker *time.Ticker }

func (w *wallClockLeaseTicker) C() <-chan time.Time { return w.ticker.C }
func (w *wallClockLeaseTicker) Stop()               { w.ticker.Stop() }

func newWallClockLeaseTicker(d time.Duration) leaseRenewalTicker {
	return &wallClockLeaseTicker{ticker: time.NewTicker(d)}
}

// taskRunBindingStore is the durable Loom surface taskBindingCoordinator
// depends on. *loom.LoomEngine implements it in production via narrow
// runtime-binding facade methods; tests may substitute a narrow wrapper to
// force a deterministic renewal/reservation failure without faking Loom's
// own clock.
type taskRunBindingStore interface {
	ReserveWorkerRunBinding(ctx context.Context, request loom.ReserveWorkerRunBindingRequest) (loom.WorkerRunBindingAuthority, error)
	StartWorkerRunBinding(ctx context.Context, request loom.StartWorkerRunBindingRequest) (loom.WorkerRunBindingAuthority, error)
	RenewWorkerRunBindingLease(ctx context.Context, request loom.RenewWorkerRunBindingLeaseRequest) (loom.WorkerRunBindingAuthority, error)
	RecordWorkerRunBindingReturned(ctx context.Context, request loom.ReturnWorkerRunBindingRequest) (loom.WorkerRunBindingAuthority, error)
	FinalizeWorkerRunBinding(ctx context.Context, request loom.FinalizeWorkerRunBindingRequest) (loom.WorkerRunBindingAuthority, error)
}

// taskBindingCoordinator is the sole server-owned seam between a generic
// task/review provider attempt and durable Worker Session / Run Binding
// authority (CR-003). Every attempt reserves Loom authority before Swarm
// acquisition/execution; direct Swarm.Get/Execute must never bypass it.
//
// ownerID is created once, at construction, and reused for every
// reserve/renew/takeover this coordinator issues — it models one coordinator
// (daemon) generation, not a per-attempt identity. BindingID and ExecutionID
// stay unique per attempt.
type taskBindingCoordinator struct {
	store   taskRunBindingStore
	swarm   *swarm.Swarm
	runtime *workerruntime.WorkerRuntime
	ownerID string

	leaseTTL       time.Duration
	renewalCadence time.Duration
	cleanupTimeout time.Duration
	newTicker      func(time.Duration) leaseRenewalTicker

	logWarn func(format string, args ...any)
}

func newTaskBindingCoordinator(store taskRunBindingStore, fabric *swarm.Swarm, runtime *workerruntime.WorkerRuntime, logWarn func(string, ...any)) *taskBindingCoordinator {
	if logWarn == nil {
		logWarn = func(string, ...any) {}
	}
	return &taskBindingCoordinator{
		store:          store,
		swarm:          fabric,
		runtime:        runtime,
		ownerID:        "coordinator-" + uuid.NewString(),
		leaseTTL:       taskBindingLeaseTTL,
		renewalCadence: taskBindingRenewalCadence,
		cleanupTimeout: taskBindingCleanupTimeout,
		newTicker:      newWallClockLeaseTicker,
		logWarn:        logWarn,
	}
}

// executeFunc performs the actual provider call against the acquired live
// binding. attempted reports whether the provider was invoked at all; see
// swarm.ExecuteSessionBinding for the exact classification boundary.
type executeFunc func(ctx context.Context, binding swarm.LiveSessionBinding, executionID types.ExecutionID) (response *types.Response, attempted bool, err error)

// dispatch reserves durable Loom authority, acquires the exact live Swarm
// binding, records live identity before execution, runs execute, then
// records or finalizes the attempt. It never silently downgrades a
// requested mode and never lets a rejected/conflicting attempt reach Swarm
// acquisition or execution.
//
// The shared attempt-lease renewal owner starts immediately after Reserve —
// before the potentially slow Swarm.AcquireSessionBinding and
// StartWorkerRunBinding calls — and its cancelable context is used for
// acquire, start, and execute. This closes a lease-expiry window where a
// slow factory could otherwise start a provider only after Loom's durable
// lease already expired underneath it. loom.StartWorkerRunBinding returns
// its input Authority unchanged (it only transitions reserved -> running
// and never rotates lease_owner/lease_generation), so the single
// Reserve-issued authority remains valid to renew for the whole attempt.
// Renewal is always stopped/joined before any finalize/return path so it
// can never race terminalization.
//
// A true native provider return (attempted == true) is recorded as
// "returned" but its lease is deliberately left active: releasing that
// authority is causally owned by Loom's terminal-outcome integration
// (tracked separately) and must never happen here. Every other exit path —
// reserve conflict, acquire failure, live-generation conversion failure,
// start failure, or a pre-provider execution rejection — closes any live
// handle this attempt owns and finalizes the reservation so the Worker
// Session is never stranded leased. If the shared renewal already failed
// before that point, the durable lease fence may already be lost to a
// takeover: the live handle is still closed so nothing leaks, but the
// durable reservation is deliberately left untouched for reconciliation
// instead of finalizing over authority this attempt may no longer hold.
func (c *taskBindingCoordinator) dispatch(ctx context.Context, cli, scope string, ident TaskBindingIdentity, session taskSessionRequest, opts []swarm.GetOption, execute executeFunc) (*types.Response, error) {
	if c == nil || c.store == nil || c.swarm == nil || c.runtime == nil {
		return nil, errors.New("task binding: coordinator unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if execute == nil {
		return nil, errors.New("task binding: execute callback is required")
	}
	requestedMode, err := session.runtimeMode()
	if err != nil {
		return nil, fmt.Errorf("task binding: %w", err)
	}
	bindingRequest := session.bindingRequest()
	if err := bindingRequest.Validate(); err != nil {
		return nil, fmt.Errorf("task binding: invalid session request: %w", err)
	}
	expectedParentProvider, err := forkParentProviderIdentity(session)
	if err != nil {
		return nil, fmt.Errorf("task binding: %w", err)
	}
	canonicalRoot, err := canonicalWorktreeRoot(scope)
	if err != nil {
		return nil, fmt.Errorf("task binding: %w", err)
	}
	tenantID := ident.TenantID

	bindingID := uuid.NewString()
	executionID := types.ExecutionID(uuid.NewString())

	authority, err := c.store.ReserveWorkerRunBinding(ctx, loom.ReserveWorkerRunBindingRequest{
		BindingID:                     bindingID,
		TaskID:                        ident.TaskID,
		WorkerSessionID:               session.WorkerSessionID,
		TenantID:                      tenantID,
		ProjectID:                     ident.ProjectID,
		CanonicalWorktreeRoot:         canonicalRoot,
		ProfileFingerprint:            ident.ProfileFingerprint,
		CapabilityFingerprint:         ident.CapabilityFingerprint,
		RequestedMode:                 requestedMode,
		ExecutorName:                  cli,
		SwarmScope:                    canonicalRoot,
		LeaseOwner:                    c.ownerID,
		LeaseTTL:                      c.leaseTTL,
		ParentWorkerSessionID:         session.ParentWorkerSessionID,
		ExpectedParentProviderSession: expectedParentProvider,
	})
	if err != nil {
		return nil, fmt.Errorf("task binding: reserve run binding: %w", err)
	}

	// Loom worker dispatch is intentionally detached from the host request,
	// so reconstruct the downstream tenant from the durable task identity.
	runCtx := tenant.WithContext(ctx, tenant.TenantContext{TenantID: tenantID})
	attemptCtx, attemptCancel := context.WithCancel(runCtx)
	renewalCtx, renewalCancel := context.WithCancel(context.WithoutCancel(runCtx))
	renewal := c.startLeaseRenewal(renewalCtx, renewalCancel, attemptCancel, authority)
	defer func() { _ = renewal.stop() }()
	var (
		binding         swarm.LiveSessionBinding
		bindingAcquired bool
	)
	defer func() {
		recovered := recover()
		if recovered == nil {
			return
		}
		if renewalErr := renewal.stop(); renewalErr != nil {
			c.logWarn("task binding: panic cleanup stopped renewal with error: %v", renewalErr)
		}
		if bindingAcquired {
			func() {
				defer func() {
					if cleanupPanic := recover(); cleanupPanic != nil {
						c.logWarn("task binding: panic cleanup itself panicked: %v", cleanupPanic)
					}
				}()
				cleanupCtx, cleanupCancel := c.detachedCleanupContext(runCtx)
				defer cleanupCancel()
				if closeErr := c.closeAcquiredLiveHandle(cleanupCtx, session, binding, authority, taskBindingReasonPanicCleanup); closeErr != nil {
					c.logWarn("task binding: panic cleanup could not close live binding: %v", closeErr)
				}
			}()
		}
		panic(recovered)
	}()

	// The coordinator-owned canonical scope is appended last so callers cannot
	// override the scope that was durably reserved above.
	acquireOpts := append(append([]swarm.GetOption{}, opts...), swarm.WithScope(canonicalRoot))
	binding, err = c.swarm.AcquireSessionBinding(attemptCtx, cli, bindingRequest, acquireOpts...)
	if err != nil {
		acquireErr := fmt.Errorf("task binding: acquire session binding: %w", err)
		return nil, c.settleUnacquiredAttempt(runCtx, renewal, session, authority, taskBindingReasonAcquireFailed, acquireErr)
	}
	bindingAcquired = true

	liveHandle, providerSession, convErr := liveHandleIdentity(binding)
	if convErr != nil {
		wrapped := fmt.Errorf("task binding: %w", convErr)
		return nil, c.settleAcquiredAttempt(runCtx, renewal, session, binding, authority, taskBindingReasonLiveGenerationInvalid, wrapped)
	}

	startedAuthority, err := c.store.StartWorkerRunBinding(attemptCtx, loom.StartWorkerRunBindingRequest{
		Authority:       authority,
		ProviderSession: providerSession,
		LiveHandle:      liveHandle,
		ExecutionID:     string(executionID),
	})
	if err != nil {
		startErr := fmt.Errorf("task binding: start run binding: %w", err)
		return nil, c.settleAcquiredAttempt(runCtx, renewal, session, binding, authority, taskBindingReasonStartFailed, startErr)
	}

	response, attempted, execErr := execute(attemptCtx, binding, executionID)
	renewalErr := renewal.stop()
	combinedErr := joinBindingErr(execErr, renewalErr)

	if !attempted {
		if combinedErr == nil {
			combinedErr = errTaskBindingProviderNotAttempted
		}
		if settleErr := c.settleRejectedLiveAttempt(runCtx, renewalErr, session, binding, startedAuthority, taskBindingReasonPreProviderRejected); settleErr != nil {
			return response, joinBindingErr(combinedErr, settleErr)
		}
		return response, combinedErr
	}
	if renewalErr != nil {
		// The provider returned, but the durable fence was lost before that
		// return could be recorded. Leave the running lease for reconciliation.
		return response, combinedErr
	}

	recordCtx, recordCancel := c.detachedCleanupContext(runCtx)
	defer recordCancel()
	if _, recordErr := c.store.RecordWorkerRunBindingReturned(recordCtx, loom.ReturnWorkerRunBindingRequest{Authority: startedAuthority}); recordErr != nil {
		wrapped := fmt.Errorf("task binding: record run binding %s returned: %w", startedAuthority.BindingID, recordErr)
		c.logWarn("%v", wrapped)
		return response, joinBindingErr(combinedErr, wrapped)
	}
	return response, combinedErr
}

// detachedCleanupContext returns a context for post-reservation cleanup
// (closing a live handle, finalizing or recording durable outcome) that
// carries ctx's values (e.g. tenant identity) but is never cancelled by
// ctx's own cancellation/deadline — the task/timeout context dispatch was
// called with may already be cancelled by the time cleanup runs, and that
// cleanup must still finish. The returned context is itself bounded so a
// genuinely hung Loom/Swarm call cannot block the caller forever; callers
// must invoke the returned cancel func (typically via defer) once cleanup
// completes.
func (c *taskBindingCoordinator) detachedCleanupContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithTimeout(context.WithoutCancel(ctx), c.cleanupTimeout)
}

// leaseRenewalHandle owns one attempt's shared cancelable context and its
// background lease-renewal goroutine. stop halts the loop, cancels the
// shared context, and reports the loop's outcome; it is idempotent — only
// the first call observes the result, so dispatch can query the outcome at
// the exact point it is needed and still defer a final safety-net stop.
type leaseRenewalHandle struct {
	renewalCancel context.CancelFunc
	attemptCancel context.CancelFunc
	done          chan struct{}
	stopCh        chan struct{}
	errCh         chan error
	once          sync.Once
}

func (c *taskBindingCoordinator) startLeaseRenewal(ctx context.Context, renewalCancel, attemptCancel context.CancelFunc, authority loom.WorkerRunBindingAuthority) *leaseRenewalHandle {
	h := &leaseRenewalHandle{
		renewalCancel: renewalCancel,
		attemptCancel: attemptCancel,
		done:          make(chan struct{}),
		stopCh:        make(chan struct{}),
		errCh:         make(chan error, 1),
	}
	go func() {
		defer close(h.done)
		c.renewLease(ctx, attemptCancel, authority, h.stopCh, h.errCh)
	}()
	return h
}

func (h *leaseRenewalHandle) stop() error {
	var err error
	h.once.Do(func() {
		close(h.stopCh)
		h.renewalCancel()
		<-h.done
		h.attemptCancel()
		select {
		case err = <-h.errCh:
		default:
		}
	})
	return err
}

// renewLease renews authority's lease every renewalCadence until stop or ctx
// ends. A renewal failure means the fence is lost — it cancels the shared
// attempt context so an unfenced attempt can never continue, reports the
// failure once on errCh, and returns.
func (c *taskBindingCoordinator) renewLease(ctx context.Context, cancel context.CancelFunc, authority loom.WorkerRunBindingAuthority, stop <-chan struct{}, errCh chan<- error) {
	ticker := c.newTicker(c.renewalCadence)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ctx.Done():
			return
		case <-ticker.C():
			if _, err := c.store.RenewWorkerRunBindingLease(ctx, loom.RenewWorkerRunBindingLeaseRequest{
				Authority: authority,
				LeaseTTL:  c.leaseTTL,
			}); err != nil {
				select {
				case <-stop:
					return
				default:
				}
				if errors.Is(err, context.Canceled) && ctx.Err() != nil {
					return
				}
				select {
				case errCh <- fmt.Errorf("renew lease: %w", err):
				default:
				}
				cancel()
				return
			}
		}
	}
}

// settleUnacquiredAttempt handles an AcquireSessionBinding failure: no live
// handle was ever obtained, so there is nothing to close. The shared
// attempt renewal is stopped first. If renewal had already failed, the
// durable lease fence may already be lost to a takeover; finalizing now
// would stamp a terminal outcome over authority reconciliation must still
// see, so the reservation is deliberately left untouched. Otherwise the
// reservation is finalized normally so a retry can reserve again.
func (c *taskBindingCoordinator) settleUnacquiredAttempt(ctx context.Context, renewal *leaseRenewalHandle, session taskSessionRequest, authority loom.WorkerRunBindingAuthority, reason string, cause error) error {
	renewalErr := renewal.stop()
	combined := joinBindingErr(cause, renewalErr)
	if renewalErr != nil {
		return combined
	}
	cleanupCtx, cleanupCancel := c.detachedCleanupContext(ctx)
	defer cleanupCancel()
	if finalizeErr := c.finalizeReservation(cleanupCtx, authority, reason, unavailableWorkerSessionState(session)); finalizeErr != nil {
		return joinBindingErr(combined, finalizeErr)
	}
	return combined
}

// settleAcquiredAttempt handles a rejection after a live handle was
// acquired but before StartWorkerRunBinding committed (a live-generation
// conversion failure, or the start write itself failing): the shared
// attempt renewal is stopped first, the live handle this attempt owns is
// always closed so nothing leaks, and the durable reservation is finalized
// only when renewal had not already failed.
func (c *taskBindingCoordinator) settleAcquiredAttempt(ctx context.Context, renewal *leaseRenewalHandle, session taskSessionRequest, binding swarm.LiveSessionBinding, authority loom.WorkerRunBindingAuthority, reason string, cause error) error {
	renewalErr := renewal.stop()
	combined := joinBindingErr(cause, renewalErr)
	cleanupCtx, cleanupCancel := c.detachedCleanupContext(ctx)
	defer cleanupCancel()
	if closeErr := c.closeAcquiredLiveHandle(cleanupCtx, session, binding, authority, reason); closeErr != nil {
		return joinBindingErr(combined, closeErr)
	}
	if renewalErr != nil {
		return combined
	}
	if finalizeErr := c.finalizeReservation(cleanupCtx, authority, reason, unavailableWorkerSessionState(session)); finalizeErr != nil {
		return joinBindingErr(combined, finalizeErr)
	}
	return combined
}

// settleRejectedLiveAttempt closes the live handle acquired for this attempt
// (when this attempt owns it) and finalizes the durable reservation,
// matching the fence-before-release contract: releasing durable authority
// while an unfenced live handle might still exist would let a second owner
// observe the Worker Session as available. exact_resume never spawned
// anything of its own — that handle is shared live authority owned by the
// Worker Session across many turns — so close is skipped for it and the
// reservation finalizes immediately. renewalErr is the outcome of the
// shared attempt-lease renewal (already stopped by the caller before this
// runs); when non-nil the durable lease fence may already be lost to a
// takeover, so the live handle is still closed but the durable reservation
// is deliberately left untouched for reconciliation instead of finalizing
// over authority this attempt may no longer hold.
func (c *taskBindingCoordinator) settleRejectedLiveAttempt(ctx context.Context, renewalErr error, session taskSessionRequest, binding swarm.LiveSessionBinding, authority loom.WorkerRunBindingAuthority, reason string) error {
	cleanupCtx, cleanupCancel := c.detachedCleanupContext(ctx)
	defer cleanupCancel()
	if closeErr := c.closeAcquiredLiveHandle(cleanupCtx, session, binding, authority, reason); closeErr != nil {
		return closeErr
	}
	if renewalErr != nil || session.Mode == types.SessionBindingModeExactResume {
		// A lost fence or a shared exact-resume handle requires reconciliation;
		// neither may be made durably available from a pre-provider rejection.
		return nil
	}
	return c.finalizeReservation(cleanupCtx, authority, reason, unavailableWorkerSessionState(session))
}

// closeAcquiredLiveHandle closes the live handle this attempt itself
// acquired. It is skipped for exact_resume, whose live handle is shared
// authority owned by the Worker Session across many turns, never this one
// attempt. The close failure (if any) is logged and returned so callers can
// surface it rather than only logging it — a close failure means durable
// authority must be left untouched for reconciliation instead of risking a
// second owner over an unfenced live handle.
func (c *taskBindingCoordinator) closeAcquiredLiveHandle(ctx context.Context, session taskSessionRequest, binding swarm.LiveSessionBinding, authority loom.WorkerRunBindingAuthority, reason string) error {
	if session.Mode == types.SessionBindingModeExactResume {
		return nil
	}
	if err := c.runtime.ReleaseSessionBinding(ctx, binding, reason); err != nil {
		wrapped := fmt.Errorf("release session binding: %w (durable run binding %s left for reconciliation)", err, authority.BindingID)
		c.logWarn("task binding: %v", wrapped)
		return wrapped
	}
	return nil
}

// finalizeReservation terminalizes the durable Run Binding. The failure (if
// any) is logged and returned so every caller can surface it instead of
// only logging it — a failed finalize must never be reported to the
// dispatch caller as a clean, fully-settled attempt.
func (c *taskBindingCoordinator) finalizeReservation(ctx context.Context, authority loom.WorkerRunBindingAuthority, reason string, workerSessionState loom.WorkerSessionState) error {
	if _, err := c.store.FinalizeWorkerRunBinding(ctx, loom.FinalizeWorkerRunBindingRequest{Authority: authority, TerminalReason: reason, WorkerSessionState: workerSessionState}); err != nil {
		wrapped := fmt.Errorf("finalize run binding %s (%s): %w", authority.BindingID, reason, err)
		c.logWarn("task binding: %v", wrapped)
		return wrapped
	}
	return nil
}

func unavailableWorkerSessionState(session taskSessionRequest) loom.WorkerSessionState {
	if session.Mode == types.SessionBindingModeNew || session.Mode == types.SessionBindingModeFork {
		return loom.WorkerSessionStateUnavailable
	}
	return ""
}

// forkParentProviderIdentity converts the Swarm parent assertion into the
// durable provider identity Loom must claim in the same transaction that
// reserves the fork child. This prevents the durable parent from changing
// after a read-only validation but before the child reservation commits.
func forkParentProviderIdentity(session taskSessionRequest) (*loom.ProviderSessionIdentity, error) {
	if session.Mode != types.SessionBindingModeFork {
		return nil, nil
	}
	if session.Parent == nil {
		return nil, fmt.Errorf("fork parent identity is required")
	}
	generation, err := int64FromGeneration("parent provider session generation", session.Parent.ProviderSession.Generation)
	if err != nil {
		return nil, err
	}
	return &loom.ProviderSessionIdentity{
		ProviderName: session.Parent.ProviderSession.Provider,
		SessionID:    session.Parent.ProviderSession.ID,
		Generation:   generation,
	}, nil
}

func joinBindingErr(first, second error) error {
	if first == nil {
		return second
	}
	if second == nil {
		return first
	}
	return fmt.Errorf("%w (also: %v)", first, second)
}

// liveHandleIdentity converts a Swarm-owned LiveSessionBinding into the exact
// live-authority shape Loom's durable Run Binding record stores.
func liveHandleIdentity(binding swarm.LiveSessionBinding) (loom.LiveHandleIdentity, *loom.ProviderSessionIdentity, error) {
	handleGeneration, err := int64FromGeneration("swarm handle generation", binding.HandleGeneration)
	if err != nil {
		return loom.LiveHandleIdentity{}, nil, err
	}
	registryGeneration, err := int64FromGeneration("swarm registry generation", binding.RegistryGeneration)
	if err != nil {
		return loom.LiveHandleIdentity{}, nil, err
	}
	live := loom.LiveHandleIdentity{
		Scope:              binding.Scope,
		HandleID:           binding.HandleID,
		HandleGeneration:   handleGeneration,
		RegistryGeneration: registryGeneration,
	}
	if binding.ProviderSession == nil {
		return live, nil, nil
	}
	generation, err := int64FromGeneration("provider session generation", binding.ProviderSession.Generation)
	if err != nil {
		return loom.LiveHandleIdentity{}, nil, err
	}
	return live, &loom.ProviderSessionIdentity{
		ProviderName: binding.ProviderSession.Provider,
		SessionID:    binding.ProviderSession.ID,
		Generation:   generation,
	}, nil
}

// int64FromGeneration converts a Swarm/provider uint64 generation into the
// int64 shape Loom's durable identity fields use. It fails closed: zero, and
// any value that would not round-trip (> math.MaxInt64), are rejected rather
// than silently wrapped into a negative number that would corrupt lease
// fencing.
func int64FromGeneration(name string, value uint64) (int64, error) {
	if value == 0 {
		return 0, fmt.Errorf("%s must be positive", name)
	}
	if value > uint64(math.MaxInt64) {
		return 0, fmt.Errorf("%s exceeds representable range", name)
	}
	return int64(value), nil
}

// canonicalWorktreeRoot derives Loom's required absolute, lexically clean
// worktree root using the current host's path semantics. Empty scope means
// the process working directory. filepath.Abs/Clean decide which separators
// and root forms are structural; filepath.ToSlash only normalizes the host
// separator for durable comparison, so a backslash remains a literal filename
// character on POSIX instead of being misread as a Windows separator.
func canonicalWorktreeRoot(scope string) (string, error) {
	root := strings.TrimSpace(scope)
	if root == "" {
		wd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("resolve process working directory: %w", err)
		}
		root = wd
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve absolute worktree root: %w", err)
	}
	root = filepath.ToSlash(filepath.Clean(abs))
	if len(root) >= 2 && root[1] == ':' && ((root[0] >= 'A' && root[0] <= 'Z') || (root[0] >= 'a' && root[0] <= 'z')) {
		root = strings.ToUpper(root[:1]) + root[1:]
	}
	return root, nil
}

// taskProfileFingerprint derives a deterministic, stable identity for the
// resolved CLI profile used for a dispatch attempt. It only needs to be
// stable and collision-resistant for compatible-vs-incompatible profile
// comparisons — it is durable evidence, never a public schema value.
func taskProfileFingerprint(cli string, profile *config.CLIProfile) string {
	h := sha256.New()
	h.Write([]byte(cli))
	h.Write([]byte{0})
	if profile != nil {
		encoded, _ := json.Marshal(profile)
		h.Write(encoded)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// taskCapabilityFingerprint derives a deterministic identity for the
// requested execution capabilities relevant to session-binding
// compatibility.
func taskCapabilityFingerprint(spec picker.TaskSpec) string {
	h := sha256.New()
	h.Write([]byte(spec.Sandbox))
	h.Write([]byte{0})
	h.Write([]byte(spec.Model))
	h.Write([]byte{0})
	h.Write([]byte(spec.Effort))
	return hex.EncodeToString(h.Sum(nil))
}
