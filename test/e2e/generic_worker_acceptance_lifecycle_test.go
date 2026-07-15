package e2e

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/thebtf/aimux/pkg/executor"
	"github.com/thebtf/aimux/pkg/executor/pipe"
	"github.com/thebtf/aimux/pkg/swarm"
	"github.com/thebtf/aimux/pkg/types"
	"github.com/thebtf/aimux/pkg/workerruntime"
)

// t016TreeEvent mirrors the "tree.node"/"tree.complete" stdout JSON emitted
// by cmd/testcli/generic_worker.go's genericWorkerTreeEvent, decoded locally
// so this package does not need to import package main.
type t016TreeEvent struct {
	Event string `json:"event"`
	Level int    `json:"level"`
	PID   int    `json:"pid"`
}

// t016LifecycleRun is one fresh WorkerRuntime -> Swarm -> CLIPipeAdapter ->
// pipe execution of a source-built generic-worker process tree, with a
// stable OS identity captured for the root, child, and grandchild while they
// were still alive, and the complete ordered event log the execution
// produced.
type t016LifecycleRun struct {
	rt     *workerruntime.WorkerRuntime
	handle *swarm.Handle
	scope  string
	execID types.ExecutionID

	mu         sync.Mutex
	events     []types.ExecutorEvent
	identities [3]*t016ProcessIdentity
	rootPID    int
	runDone    chan error
}

// waitDone blocks for the in-flight Execute goroutine to return, bounding the
// wait so a stuck execution fails the test instead of hanging it.
func (run *t016LifecycleRun) waitDone(t *testing.T, scenarioID string, timeout time.Duration) error {
	t.Helper()
	select {
	case err := <-run.runDone:
		return err
	case <-time.After(timeout):
		t.Fatalf("scenario %q: execution did not return within %s", scenarioID, timeout)
		return nil
	}
}

// snapshotAndDeriveTerminal copies the complete ordered event log and
// derives the sole terminal event, failing the test if zero or multiple
// terminal events were admitted, or if the terminal is not the final event
// in the log (no output can truthfully trail an execution's own terminal
// admission).
func (run *t016LifecycleRun) snapshotAndDeriveTerminal(t *testing.T, scenarioID string) ([]types.ExecutorEvent, types.ExecutorEvent) {
	t.Helper()

	run.mu.Lock()
	snapshot := append([]types.ExecutorEvent(nil), run.events...)
	run.mu.Unlock()

	terminalIndex := -1
	var terminal types.ExecutorEvent
	for index, event := range snapshot {
		if !event.Terminal {
			continue
		}
		if terminalIndex >= 0 {
			t.Fatalf("scenario %q emitted multiple terminal events: %#v", scenarioID, snapshot)
		}
		terminalIndex = index
		terminal = event
	}
	if terminalIndex < 0 {
		t.Fatalf("scenario %q emitted no terminal event: %#v", scenarioID, snapshot)
	}
	if terminalIndex != len(snapshot)-1 {
		t.Fatalf("scenario %q terminal index = %d, want final event %d: %#v", scenarioID, terminalIndex, len(snapshot)-1, snapshot)
	}
	return snapshot, terminal
}

// assertAllIdentitiesDead proves every captured identity (root, child, and
// grandchild) is actually gone via the stable per-platform handle, not via
// os.FindProcess or the parent's own exit status alone.
func (run *t016LifecycleRun) assertAllIdentitiesDead(t *testing.T, scenarioID string, timeout time.Duration) {
	t.Helper()
	for level, identity := range run.identities {
		if !waitForT016ProcessExit(identity, timeout) {
			t.Fatalf("scenario %q: level %d process did not exit within %s", scenarioID, level, timeout)
		}
	}
}

// startT016LifecycleRun launches a fresh public runtime path (WorkerRuntime
// over a Swarm-owned CLIPipeAdapter/pipe executor), spawns the source-built
// fixture named by spec.Input.Args/TimeoutSeconds, and blocks until root,
// child, and grandchild PIDs are known from fragmented stdout JSON. It
// captures a stable identity for each while still alive and registers
// cleanup (via t.Cleanup, so it still runs after a later fatal assertion)
// that force-kills and closes every identity and shuts the swarm down,
// surfacing any cleanup failure through t.Errorf rather than swallowing it.
func startT016LifecycleRun(t *testing.T, binary string, spec t016ScenarioSpec) *t016LifecycleRun {
	t.Helper()

	adapter := executor.NewCLIPipeAdapter(pipe.New())
	s := swarm.New(func(string) (types.ExecutorV2, error) { return adapter, nil }, nil)
	t.Cleanup(func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		if err := s.Shutdown(shutdownCtx); err != nil {
			t.Errorf("scenario %q: shutdown swarm: %v", spec.ID, err)
		}
	})

	scope := "t016-lifecycle-" + spec.ID
	execID := types.ExecutionID("t016-lifecycle-" + spec.ID)
	handle, err := s.Get(context.Background(), "pipe", swarm.Stateful, swarm.WithScope(scope))
	if err != nil {
		t.Fatalf("scenario %q: get scoped pipe handle: %v", spec.ID, err)
	}
	rt, err := workerruntime.New(s)
	if err != nil {
		t.Fatalf("scenario %q: create worker runtime: %v", spec.ID, err)
	}

	run := &t016LifecycleRun{
		rt:      rt,
		handle:  handle,
		scope:   scope,
		execID:  execID,
		runDone: make(chan error, 1),
	}

	args := append([]string(nil), spec.Input.Args...)
	msg := types.Message{Spawn: &types.SpawnArgs{
		Command:        binary,
		Args:           args,
		TimeoutSeconds: spec.Input.TimeoutSeconds,
	}}

	pending := ""
	pids := make(map[int]int)
	nodesReady := make(chan struct{})
	var nodesOnce sync.Once

	go func() {
		_, execErr := rt.Execute(context.Background(), handle, scope, execID, msg, types.ExecutorEventSinkFunc(func(event types.ExecutorEvent) bool {
			event.Content = append([]byte(nil), event.Content...)
			run.mu.Lock()
			defer run.mu.Unlock()
			run.events = append(run.events, event)
			if event.Terminal || event.Channel != "stdout" {
				return true
			}
			pending += string(event.Content)
			for {
				newline := strings.IndexByte(pending, '\n')
				if newline < 0 {
					break
				}
				line, rest := pending[:newline], pending[newline+1:]
				pending = rest
				var node t016TreeEvent
				if json.Unmarshal([]byte(line), &node) == nil && node.Event == "tree.node" && node.Level >= 0 && node.Level <= 2 && node.PID > 0 {
					pids[node.Level] = node.PID
					if len(pids) == 3 {
						nodesOnce.Do(func() { close(nodesReady) })
					}
				}
			}
			return true
		}))
		run.runDone <- execErr
	}()

	select {
	case <-nodesReady:
	case <-time.After(30 * time.Second):
		t.Fatalf("scenario %q: generic-worker did not emit root, child, and grandchild identities", spec.ID)
	}

	run.mu.Lock()
	for level := range 3 {
		identity, captureErr := captureT016ProcessIdentity(pids[level])
		if captureErr != nil || identity == nil || !t016ProcessAlive(identity) {
			run.mu.Unlock()
			t.Fatalf("scenario %q: capture level %d pid %d: identity=%#v err=%v", spec.ID, level, pids[level], identity, captureErr)
		}
		run.identities[level] = identity
		t.Cleanup(func() {
			if killErr := t016ForceKillProcess(identity); killErr != nil {
				t.Errorf("scenario %q: force-cleanup level %d pid %d: %v", spec.ID, level, identity.pid, killErr)
			}
			closeT016ProcessIdentity(identity)
		})
	}
	run.rootPID = pids[0]
	run.mu.Unlock()

	return run
}

// runT016CancelTreeScenario proves WorkerRuntime.Cancel stops a source-built
// generic-worker process tree end to end: bounded Execute return, exactly
// one truthful "cancelled" terminal as the final event of the complete
// ordered log, validated ownership-boundary ProcessTreeEvidence (stopped,
// structurally valid, root PID) via Inspect, immutable Inspect/terminal
// state on a later re-look, and every captured identity (root, child,
// grandchild) actually dead. Shared by the "termination" and
// "tree_liveness" proofs (manifest IDs "cancel" and
// "source_built_zero_child_leak"), which both call WorkerRuntime.Cancel
// against the identical tree fixture -- the latter additionally proves via
// assertAllIdentitiesDead that no descendant leaks past the root.
func runT016CancelTreeScenario(t *testing.T, binary string, spec t016ScenarioSpec) string {
	t.Helper()

	run := startT016LifecycleRun(t, binary, spec)

	evidence, cancelErr := run.rt.Cancel(context.Background(), run.handle, run.scope, run.execID, spec.ID)
	if cancelErr != nil {
		t.Fatalf("scenario %q: WorkerRuntime.Cancel: %v", spec.ID, cancelErr)
	}
	if evidence.ExecutionID != run.execID || evidence.NativeAcknowledged {
		t.Fatalf("scenario %q: CancellationEvidence = %#v, want ExecutionID=%q NativeAcknowledged=false", spec.ID, evidence, run.execID)
	}

	if execErr := run.waitDone(t, spec.ID, 30*time.Second); execErr != nil {
		t.Fatalf("scenario %q: Execute returned error: %v", spec.ID, execErr)
	}

	_, terminal := run.snapshotAndDeriveTerminal(t, spec.ID)
	if terminal.Type != spec.Expected {
		t.Fatalf("scenario %q: terminal type = %q, want %q", spec.ID, terminal.Type, spec.Expected)
	}

	inspection, err := run.rt.Inspect(context.Background(), run.handle, run.scope, run.execID)
	if err != nil {
		t.Fatalf("scenario %q: Inspect: %v", spec.ID, err)
	}
	if !inspection.Terminal || !inspection.Cancelled {
		t.Fatalf("scenario %q: Inspect Terminal/Cancelled = %#v, want Terminal=true Cancelled=true", spec.ID, inspection)
	}
	tree := inspection.ProcessTreeEvidence
	if !tree.Stopped || tree.Validate() != nil || tree.Process.PID != run.rootPID {
		t.Fatalf("scenario %q: ProcessTreeEvidence = %#v, want stopped+valid root pid %d", spec.ID, tree, run.rootPID)
	}

	run.assertAllIdentitiesDead(t, spec.ID, 5*time.Second)

	// No terminal/Inspect mutation on a later re-Inspect: the execution is
	// already fenced terminal, so a second look must be byte-for-byte the
	// same evidence, not a fresh or evolving snapshot.
	time.Sleep(50 * time.Millisecond)
	lateInspection, err := run.rt.Inspect(context.Background(), run.handle, run.scope, run.execID)
	if err != nil || lateInspection != inspection {
		t.Fatalf("scenario %q: late Inspect = %#v, %v; want immutable %#v", spec.ID, lateInspection, err, inspection)
	}
	_, lateTerminal := run.snapshotAndDeriveTerminal(t, spec.ID)
	if lateTerminal.Type != terminal.Type || lateTerminal.Terminal != terminal.Terminal || lateTerminal.Truncated != terminal.Truncated {
		t.Fatalf("scenario %q: late terminal mutation: %#v, want %#v", spec.ID, lateTerminal, terminal)
	}

	return terminal.Type
}

// runT016TimeoutTreeScenario proves a typed SpawnArgs.TimeoutSeconds
// deadline (spec.Input.TimeoutSeconds) stops a source-built generic-worker
// process tree end to end: bounded Execute return, exactly one truthful
// "timeout" terminal as the final event of the complete ordered log,
// validated ownership-boundary ProcessTreeEvidence (stopped, structurally
// valid, root PID) via Inspect, and every captured identity (root, child,
// grandchild) actually dead. No WorkerRuntime.Cancel call is made -- the
// executor's own deadline is what tears the tree down.
func runT016TimeoutTreeScenario(t *testing.T, binary string, spec t016ScenarioSpec) string {
	t.Helper()

	run := startT016LifecycleRun(t, binary, spec)

	if execErr := run.waitDone(t, spec.ID, 30*time.Second); execErr != nil {
		t.Fatalf("scenario %q: Execute returned error: %v", spec.ID, execErr)
	}

	_, terminal := run.snapshotAndDeriveTerminal(t, spec.ID)
	if terminal.Type != spec.Expected {
		t.Fatalf("scenario %q: terminal type = %q, want %q", spec.ID, terminal.Type, spec.Expected)
	}

	inspection, err := run.rt.Inspect(context.Background(), run.handle, run.scope, run.execID)
	if err != nil {
		t.Fatalf("scenario %q: Inspect: %v", spec.ID, err)
	}
	if !inspection.Terminal || inspection.Cancelled {
		t.Fatalf("scenario %q: Inspect Terminal/Cancelled = %#v, want Terminal=true Cancelled=false", spec.ID, inspection)
	}
	tree := inspection.ProcessTreeEvidence
	if !tree.Stopped || tree.Validate() != nil || tree.Process.PID != run.rootPID {
		t.Fatalf("scenario %q: ProcessTreeEvidence = %#v, want stopped+valid root pid %d", spec.ID, tree, run.rootPID)
	}

	run.assertAllIdentitiesDead(t, spec.ID, 5*time.Second)

	return terminal.Type
}

// runT016LifecycleScenario is the fixed T016 dispatch entry point for the
// "termination", "deadline", and "tree_liveness" proofs (manifest IDs
// "cancel", "timeout", and "source_built_zero_child_leak"). It returns the
// exact terminal classification string the caller compares against
// spec.Expected.
func runT016LifecycleScenario(t *testing.T, binary string, spec t016ScenarioSpec) string {
	t.Helper()

	switch spec.Proof {
	case "termination", "tree_liveness":
		return runT016CancelTreeScenario(t, binary, spec)
	case "deadline":
		return runT016TimeoutTreeScenario(t, binary, spec)
	default:
		t.Fatalf("runT016LifecycleScenario: unsupported proof %q", spec.Proof)
		return ""
	}
}
