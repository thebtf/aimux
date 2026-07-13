package swarm

import (
	"context"
	"os"
	"testing"

	aimexecutor "github.com/thebtf/aimux/pkg/executor"
	pipeexecutor "github.com/thebtf/aimux/pkg/executor/pipe"
	"github.com/thebtf/aimux/pkg/types"
)

const ownedLeaseHelperEnv = "AIMUX_SWARM_OWNED_LEASE_HELPER"
const ownedLeaseFileEnv = "AIMUX_SWARM_OWNED_LEASE_FILE"

func TestSwarmOwnedLeaseHelper(t *testing.T) {
	if os.Getenv(ownedLeaseHelperEnv) != "1" {
		return
	}
	if err := os.WriteFile(os.Getenv(ownedLeaseFileEnv), []byte("started"), 0o600); err != nil {
		os.Exit(2)
	}
	os.Exit(0)
}

func ownedLeaseMessage(file string) types.Message {
	return types.Message{Spawn: &types.SpawnArgs{
		Command: os.Args[0],
		Args:    []string{"-test.run=^TestSwarmOwnedLeaseHelper$", "-test.count=1"},
		Env: map[string]string{
			ownedLeaseHelperEnv: "1",
			ownedLeaseFileEnv:   file,
		},
	}}
}

func TestSwarmOwnedLeaseRejectsDirectSameIDBeforeSideEffect(t *testing.T) {
	pipe := pipeexecutor.New()
	adapter := aimexecutor.NewCLIPipeAdapter(pipe)
	s := New(func(string) (types.ExecutorV2, error) { return adapter, nil }, nil)
	h, err := s.Get(context.Background(), "owned-lease", Stateful, WithScope("scope"))
	if err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	previous := beforeOwnedLeaseExecution
	beforeOwnedLeaseExecution = func() {
		close(entered)
		<-release
	}
	t.Cleanup(func() { beforeOwnedLeaseExecution = previous })
	ownerFile := t.TempDir() + string(os.PathSeparator) + "owner"
	attackerFile := t.TempDir() + string(os.PathSeparator) + "attacker"
	done := make(chan struct{})
	go func() {
		_, _ = s.Execute(context.Background(), h, "scope", "owned-lease", ownedLeaseMessage(ownerFile), types.ExecutorEventSinkFunc(func(types.ExecutorEvent) bool { return true }))
		close(done)
	}()
	<-entered
	if _, err := adapter.SendEvents(context.Background(), "owned-lease", ownedLeaseMessage(attackerFile), nil); err == nil {
		t.Fatal("direct caller consumed Swarm-owned lease")
	}
	if _, err := os.Stat(attackerFile); !os.IsNotExist(err) {
		t.Fatalf("direct caller performed side effect: %v", err)
	}
	close(release)
	<-done
	if _, err := os.Stat(ownerFile); err != nil {
		t.Fatalf("owner did not start its leased helper: %v", err)
	}
}
