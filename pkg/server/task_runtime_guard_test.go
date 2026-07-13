package server

import (
	"errors"
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestTaskWorkerRuntimeRejectsAfterShutdownGate(t *testing.T) {
	s := &Server{taskRuntimeClosed: true}
	if _, _, err := s.taskWorkerRuntime(); !errors.Is(err, errTaskRuntimeClosed) {
		t.Fatalf("taskWorkerRuntime after shutdown = %v, want errTaskRuntimeClosed", err)
	}
}

func TestTaskDispatchUsesSwarmWorkerRuntimeAndBoundedEventWriter(t *testing.T) {
	taskTool, err := os.ReadFile("task_tool.go")
	if err != nil {
		t.Fatal(err)
	}
	toolText := string(taskTool)
	if strings.Contains(toolText, "pipeExec.New()") || strings.Contains(toolText, ".Run(ctx, spawnArgs)") || strings.Contains(toolText, "LegacyRun") {
		t.Fatal("taskDispatch must not bypass WorkerRuntime.Execute with direct or legacy execution")
	}

	runtimeBridge, err := os.ReadFile("task_dispatch_runtime.go")
	if err != nil {
		t.Fatal(err)
	}
	bridgeText := string(runtimeBridge)
	for _, required := range []string{"taskRuntime.Execute", "swarm.WithScope"} {
		if !strings.Contains(bridgeText, required) {
			t.Fatalf("runtime bridge missing %q", required)
		}
	}
	if !regexp.MustCompile(`Spawn:\s*&spawnArgs`).MatchString(bridgeText) {
		t.Fatal("runtime bridge must preserve the typed SpawnArgs carrier")
	}

	workers, err := os.ReadFile("task_workers.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(workers), "workerruntime.NewExecutorEventSink") {
		t.Fatal("production task worker must bind the existing EventWriter/eventPump to runtime execution")
	}

	serverSource, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatal(err)
	}
	serverText := string(serverSource)
	closedAt := strings.Index(serverText, "taskRuntimeClosed = true")
	quiesceAt := strings.Index(serverText, "taskSwarm.BeginShutdown()")
	drainAt := strings.Index(serverText, "executor.SharedPM.GracefulShutdown")
	if closedAt < 0 || quiesceAt < 0 || drainAt < 0 || closedAt > drainAt || quiesceAt > drainAt {
		t.Fatal("task admission must close before the ProcessManager graceful-drain snapshot")
	}
}
