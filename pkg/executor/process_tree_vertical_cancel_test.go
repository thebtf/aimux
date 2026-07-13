package executor_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/thebtf/aimux/pkg/executor"
	"github.com/thebtf/aimux/pkg/executor/pipe"
	"github.com/thebtf/aimux/pkg/swarm"
	"github.com/thebtf/aimux/pkg/types"
)

type verticalTreeEvent struct {
	Event string `json:"event"`
	Level int    `json:"level"`
	PID   int    `json:"pid"`
}

func TestSwarmCLIPipeAdapterCancelStopsGenericWorkerTree(t *testing.T) {
	binary := buildGenericWorker(t)
	baselineGoroutines := runtime.NumGoroutine()
	adapter := executor.NewCLIPipeAdapter(pipe.New())
	s := swarm.New(func(string) (types.ExecutorV2, error) { return adapter, nil }, nil)
	const scope = "tree-scope"
	h, err := s.Get(context.Background(), "pipe", swarm.Stateful, swarm.WithScope(scope))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.Shutdown(shutdownCtx)
	}()

	var mu sync.Mutex
	var pending string
	pids := make(map[int]int)
	nodesReady := make(chan struct{})
	var nodesOnce sync.Once
	terminals := make([]types.ExecutorEvent, 0, 1)
	runDone := make(chan error, 1)
	go func() {
		_, runErr := s.Execute(context.Background(), h, scope, "vertical-tree", types.Message{Spawn: &types.SpawnArgs{
			Command: binary,
			Args:    []string{"generic-worker", "--mode", "tree", "--depth", "2", "--hold-ms", "10000"},
		}}, types.ExecutorEventSinkFunc(func(event types.ExecutorEvent) bool {
			mu.Lock()
			defer mu.Unlock()
			if event.Terminal {
				terminals = append(terminals, event)
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
				var node verticalTreeEvent
				if json.Unmarshal([]byte(line), &node) == nil && node.Event == "tree.node" && node.Level >= 0 && node.Level <= 2 && node.PID > 0 {
					pids[node.Level] = node.PID
					if len(pids) == 3 {
						nodesOnce.Do(func() { close(nodesReady) })
					}
				}
			}
			return true
		}))
		runDone <- runErr
	}()
	select {
	case <-nodesReady:
	case <-time.After(30 * time.Second):
		t.Fatal("generic-worker did not emit root, child, and grandchild identities")
	}

	identities := make([]*processTreeIdentity, 3)
	mu.Lock()
	for level := range identities {
		var captureErr error
		identities[level], captureErr = captureProcessTreeIdentity(pids[level])
		if captureErr != nil || identities[level] == nil || !processTreeProcessAlive(identities[level]) {
			mu.Unlock()
			t.Fatalf("capture level %d pid %d: identity=%#v err=%v", level, pids[level], identities[level], captureErr)
		}
	}
	rootPID := pids[0]
	mu.Unlock()
	defer func() {
		for _, identity := range identities {
			_ = processTreeForceKill(identity)
			closeProcessTreeIdentity(identity)
		}
	}()

	type cancelResult struct {
		evidence types.CancellationEvidence
		err      error
	}
	first := make(chan cancelResult, 1)
	repeated := make(chan cancelResult, 1)
	inspectionDone := make(chan struct {
		inspection swarm.ExecutionInspection
		err        error
	}, 1)
	go func() {
		evidence, cancelErr := s.Cancel(context.Background(), h, scope, "vertical-tree", "first")
		first <- cancelResult{evidence, cancelErr}
	}()
	if !waitForProcessTreeExit(identities[0], 5*time.Second) {
		t.Fatal("first Cancel did not begin terminating the root process")
	}
	go func() {
		evidence, cancelErr := s.Cancel(context.Background(), h, scope, "vertical-tree", "repeat")
		repeated <- cancelResult{evidence, cancelErr}
	}()
	go func() {
		inspection, inspectErr := s.Inspect(context.Background(), h, scope, "vertical-tree")
		inspectionDone <- struct {
			inspection swarm.ExecutionInspection
			err        error
		}{inspection, inspectErr}
	}()

	firstResult := <-first
	repeatedResult := <-repeated
	inspectionResult := <-inspectionDone
	if firstResult.err != nil || repeatedResult.err != nil || firstResult.evidence != repeatedResult.evidence || firstResult.evidence.NativeAcknowledged {
		t.Fatalf("Cancel results first=%#v,%v repeated=%#v,%v", firstResult.evidence, firstResult.err, repeatedResult.evidence, repeatedResult.err)
	}
	if inspectionResult.err != nil || !inspectionResult.inspection.Cancelled || !inspectionResult.inspection.ProcessTreeEvidence.Stopped || inspectionResult.inspection.ProcessTreeEvidence.Process.Validate() != nil || inspectionResult.inspection.ProcessTreeEvidence.Process.PID != rootPID {
		t.Fatalf("Inspect = %#v, %v", inspectionResult.inspection, inspectionResult.err)
	}
	for level, identity := range identities {
		if processTreeProcessAlive(identity) {
			t.Fatalf("Inspect reported stopped before generic-worker level %d exited", level)
		}
	}
	if err := <-runDone; err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	if len(terminals) != 1 || terminals[0].Type != "cancelled" || !terminals[0].Terminal {
		mu.Unlock()
		t.Fatalf("terminals = %#v", terminals)
	}
	terminal := terminals[0]
	mu.Unlock()
	time.Sleep(50 * time.Millisecond)
	inspection, err := s.Inspect(context.Background(), h, scope, "vertical-tree")
	if err != nil || inspection != inspectionResult.inspection {
		t.Fatalf("late Inspect = %#v, %v; want immutable %#v", inspection, err, inspectionResult.inspection)
	}
	mu.Lock()
	lateTerminals := append([]types.ExecutorEvent(nil), terminals...)
	mu.Unlock()
	if len(lateTerminals) != 1 || lateTerminals[0].Type != terminal.Type || lateTerminals[0].Terminal != terminal.Terminal || lateTerminals[0].Truncated != terminal.Truncated {
		t.Fatalf("late terminal mutation: %#v", lateTerminals)
	}
	if runtime.NumGoroutine() > baselineGoroutines+4 {
		t.Fatalf("goroutines grew from %d to %d", baselineGoroutines, runtime.NumGoroutine())
	}
}

func buildGenericWorker(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	repo := filepath.Dir(filepath.Dir(wd))
	binary := filepath.Join(t.TempDir(), "testcli")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	command := exec.Command("go", "build", "-o", binary, "./cmd/testcli")
	command.Dir = repo
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build generic-worker: %v\n%s", err, output)
	}
	return binary
}
