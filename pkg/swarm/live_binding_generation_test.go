package swarm

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

const registryGenerationHelperEnv = "AIMUX_SWARM_REGISTRY_GENERATION_HELPER"

func TestSwarmRegistryGenerationChangesAcrossProcesses(t *testing.T) {
	if os.Getenv(registryGenerationHelperEnv) == "1" {
		s := New(nil, nil, WithStatefulTTL(0))
		fmt.Print(s.registryGeneration)
		_ = s.Shutdown(context.Background())
		os.Exit(0)
	}

	readGeneration := func() uint64 {
		t.Helper()
		cmd := exec.Command(os.Args[0], "-test.run=^TestSwarmRegistryGenerationChangesAcrossProcesses$")
		cmd.Env = append(os.Environ(), registryGenerationHelperEnv+"=1")
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("registry generation helper: %v\n%s", err, output)
		}
		generation, err := strconv.ParseUint(strings.TrimSpace(string(output)), 10, 64)
		if err != nil {
			t.Fatalf("parse registry generation %q: %v", output, err)
		}
		if generation == 0 || generation > uint64(^uint64(0)>>1) {
			t.Fatalf("registry generation = %d, want positive SQLite INTEGER", generation)
		}
		return generation
	}

	first := readGeneration()
	second := readGeneration()
	if first == second {
		t.Fatalf("fresh process registry generations both = %d; stale durable handles could alias after restart", first)
	}
}
