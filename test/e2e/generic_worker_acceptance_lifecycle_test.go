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

// t016LifecycleTimeoutSeconds is the typed SpawnArgs.TimeoutSeconds budget for
// the "timeout" scenario. It stays well below the generic-worker tree's 10s
// leaf hold (so the executor's own deadline fires first) and well above the
// sub-second time root/child/grandchild identities normally take to announce
// themselves on stdout before that leaf sleep begins.
const t016LifecycleTimeoutSeconds = 3

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
// were still alive.
type t016LifecycleRun struct {
	rt     *workerruntime.WorkerRuntime
	handle *swarm.Handle
	scope  string

	mu         sync.Mutex
	terminals  []types.ExecutorEvent
	identities [3]*t016ProcessIdentity
	rootPID    int
	runDone    chan error
}

func (run *t016LifecycleRun) snapshotTerminals() []types.ExecutorEvent {
	run.mu.Lock()
	defer run.mu.Unlock()
	return append([]types.ExecutorEvent(nil), run.terminals...)
}

// waitDone blocks for the in-flight Execute goroutine to return, bounding the
// wait so a stuck execution fails the test instead of hanging it.
func (run *t016LifecycleRun) waitDone(t *testing.T, timeout time.Duration) error {
	t.Helper()
	select {
	case err := <-run.runDone:
		return err
	case <-time.After(timeout):
		t.Fatalf("execution did not return within %s", timeout)
		return nil
	}
}

// assertAllIdentitiesDead proves every captured identity (root, child, and
// grandchild) is actually gone via the stable per-platform handle, not via
// os.FindProcess or the parent's own exit status alone.
func (run *t016LifecycleRun) assertAllIdentitiesDead(t *testing.T, timeout time.Duration) {
	t.Helper()
	for level, identity := range run.identities {
		if !waitForT016ProcessExit(identity, timeout) {
			t.Fatalf("level %d process did not exit within %s", level, timeout)
		}
	}
}

// startT016LifecycleRun launches a fresh public runtime path (WorkerRuntime
// over a Swarm-owned CLIPipeAdapter/pipe executor), spawns a source-built
// "generic-worker --mode tree --depth 2 --hold-ms 10000" process tree, and
// blocks until root, child, and grandchild PIDs are known from fragmented
// stdout JSON. It captures a stable identity for each while still alive and
// registers cleanup (via t.Cleanup, so it still runs after a later fatal
// assertion) that force-kills and closes every identity and shuts the swarm
// down.
func startT016LifecycleRun(t *testing.T, binary string, execID types.ExecutionID, timeoutSeconds int) *t016LifecycleRun {
	t.Helper()

	adapter := executor.NewCLIPipeAdapter(pipe.New())
	s := swarm.New(func(string) (types.ExecutorV2, error) { return adapter, nil }, nil)
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.Shutdown(shutdownCtx)
	})

	const scope = "t016-lifecycle-scope"
	handle, err := s.Get(context.Background(), "pipe", swarm.Stateful, swarm.WithScope(scope))
	if err != nil {
		t.Fatalf("acquire swarm handle: %v", err)
	}
	rt, err := workerruntime.New(s)
	if err != nil {
		t.Fatalf("create worker runtime: %v", err)
	}

	run := &t016LifecycleRun{
		rt:      rt,
		handle:  handle,
		scope:   scope,
		runDone: make(chan error, 1),
	}

	msg := types.Message{Spawn: &types.SpawnArgs{
		Command:        binary,
		Args:           []string{"generic-worker", "--mode", "tree", "--depth", "2", "--hold-ms", "10000"},
		TimeoutSeconds: timeoutSeconds,
	}}

	pending := ""
	pids := make(map[int]int)
	nodesReady := make(chan struct{})
	var nodesOnce sync.Once

	go func() {
		_, execErr := rt.Execute(context.Background(), handle, scope, execID, msg, types.ExecutorEventSinkFunc(func(event types.ExecutorEvent) bool {
			run.mu.Lock()
			defer run.mu.Unlock()
			if event.Terminal {
				run.terminals = append(run.terminals, event)
				return true
			}
			if event.Channel != "stdout" {
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
		t.Fatal("generic-worker did not emit root, child, and grandchild identities")
	}

	run.mu.Lock()
	for level := range 3 {
		identity, captureErr := captureT016ProcessIdentity(pids[level])
		if captureErr != nil || identity == nil || !t016ProcessAlive(identity) {
			run.mu.Unlock()
			t.Fatalf("capture level %d pid %d: identity=%#v err=%v", level, pids[level], identity, captureErr)
		}
		run.identities[level] = identity
		t.Cleanup(func() {
			_ = t016ForceKillProcess(identity)
			closeT016ProcessIdentity(identity)
		})
	}
	run.rootPID = pids[0]
	run.mu.Unlock()

	return run
}

// runT016CancelTreeScenario proves WorkerRuntime.Cancel stops a source-built
// generic-worker process tree end to end: bounded Execute return, exactly
// one truthful "cancelled" terminal, valid stopped process-tree evidence for
// the root, every captured identity (root, child, grandchild) actually
// dead, and no terminal/Inspect mutation on a later re-Inspect. Shared by
// the "cancel" and "source_built_zero_child_leak" manifest rows, which both
// call WorkerRuntime.Cancel against the identical tree fixture — the latter
// additionally proves via assertAllIdentitiesDead that no descendant leaks
// past the root.
func runT016CancelTreeScenario(t *testing.T, binary, scenarioID string) string {
	t.Helper()

	execID := types.ExecutionID("t016-" + scenarioID)
	run := startT016LifecycleRun(t, binary, execID, 0)

	evidence, cancelErr := run.rt.Cancel(context.Background(), run.handle, run.scope, execID, scenarioID)
	if cancelErr != nil {
		t.Fatalf("%s: WorkerRuntime.Cancel: %v", scenarioID, cancelErr)
	}
	if evidence.ExecutionID != execID || evidence.NativeAcknowledged {
		t.Fatalf("%s: CancellationEvidence = %#v, want ExecutionID=%q NativeAcknowledged=false", scenarioID, evidence, execID)
	}

	if execErr := run.waitDone(t, 30*time.Second); execErr != nil {
		t.Fatalf("%s: Execute returned error: %v", scenarioID, execErr)
	}

	terminals := run.snapshotTerminals()
	if len(terminals) != 1 || !terminals[0].Terminal || terminals[0].Type != "cancelled" {
		t.Fatalf("%s: terminals = %#v, want exactly one cancelled terminal", scenarioID, terminals)
	}
	terminal := terminals[0]

	inspection, err := run.rt.Inspect(context.Background(), run.handle, run.scope, execID)
	if err != nil {
		t.Fatalf("%s: Inspect: %v", scenarioID, err)
	}
	if !inspection.Terminal || !inspection.Cancelled {
		t.Fatalf("%s: Inspect Terminal/Cancelled = %#v", scenarioID, inspection)
	}
	tree := inspection.ProcessTreeEvidence
	if !tree.Stopped || tree.Validate() != nil || tree.Process.PID != run.rootPID {
		t.Fatalf("%s: ProcessTreeEvidence = %#v, want stopped+valid root pid %d", scenarioID, tree, run.rootPID)
	}

	run.assertAllIdentitiesDead(t, 5*time.Second)

	// No terminal/Inspect mutation on a later re-Inspect: the execution is
	// already fenced terminal, so a second look must be byte-for-byte the
	// same evidence, not a fresh or evolving snapshot.
	time.Sleep(50 * time.Millisecond)
	lateInspection, err := run.rt.Inspect(context.Background(), run.handle, run.scope, execID)
	if err != nil || lateInspection != inspection {
		t.Fatalf("%s: late Inspect = %#v, %v; want immutable %#v", scenarioID, lateInspection, err, inspection)
	}
	lateTerminals := run.snapshotTerminals()
	if len(lateTerminals) != 1 ||
		lateTerminals[0].Type != terminal.Type ||
		lateTerminals[0].Terminal != terminal.Terminal ||
		lateTerminals[0].Truncated != terminal.Truncated {
		t.Fatalf("%s: late terminal mutation: %#v, want %#v", scenarioID, lateTerminals, terminal)
	}

	return terminal.Type
}

// runT016TimeoutTreeScenario proves a typed SpawnArgs.TimeoutSeconds deadline
// stops a source-built generic-worker process tree end to end: bounded
// Execute return, exactly one truthful "timeout" terminal, valid stopped
// process-tree evidence for the root, and every captured identity (root,
// child, grandchild) actually dead. No WorkerRuntime.Cancel call is made —
// the executor's own deadline is what tears the tree down.
func runT016TimeoutTreeScenario(t *testing.T, binary, scenarioID string) string {
	t.Helper()

	execID := types.ExecutionID("t016-" + scenarioID)
	run := startT016LifecycleRun(t, binary, execID, t016LifecycleTimeoutSeconds)

	if execErr := run.waitDone(t, 30*time.Second); execErr != nil {
		t.Fatalf("%s: Execute returned error: %v", scenarioID, execErr)
	}

	terminals := run.snapshotTerminals()
	if len(terminals) != 1 || !terminals[0].Terminal || terminals[0].Type != "timeout" {
		t.Fatalf("%s: terminals = %#v, want exactly one timeout terminal", scenarioID, terminals)
	}
	terminal := terminals[0]

	inspection, err := run.rt.Inspect(context.Background(), run.handle, run.scope, execID)
	if err != nil {
		t.Fatalf("%s: Inspect: %v", scenarioID, err)
	}
	if !inspection.Terminal || inspection.Cancelled {
		t.Fatalf("%s: Inspect Terminal/Cancelled = %#v, want Terminal=true Cancelled=false", scenarioID, inspection)
	}
	tree := inspection.ProcessTreeEvidence
	if !tree.Stopped || tree.Validate() != nil || tree.Process.PID != run.rootPID {
		t.Fatalf("%s: ProcessTreeEvidence = %#v, want stopped+valid root pid %d", scenarioID, tree, run.rootPID)
	}

	run.assertAllIdentitiesDead(t, 5*time.Second)

	return terminal.Type
}

// runT016LifecycleScenario is the fixed T016 dispatch entry point for the
// "cancel", "timeout", and "source_built_zero_child_leak" manifest rows. It
// returns the exact terminal classification string the caller compares
// against spec.Expected.
func runT016LifecycleScenario(t *testing.T, binary string, spec t016ScenarioSpec) string {
	t.Helper()

	switch spec.ID {
	case "cancel", "source_built_zero_child_leak":
		return runT016CancelTreeScenario(t, binary, spec.ID)
	case "timeout":
		return runT016TimeoutTreeScenario(t, binary, spec.ID)
	default:
		t.Fatalf("runT016LifecycleScenario: unsupported scenario ID %q", spec.ID)
		return ""
	}
}
