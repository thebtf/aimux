package server

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

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
}
