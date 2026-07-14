package swarm

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	aimexecutor "github.com/thebtf/aimux/pkg/executor"
	pipeexecutor "github.com/thebtf/aimux/pkg/executor/pipe"
	"github.com/thebtf/aimux/pkg/types"
)

const (
	ownedLeaseHelperEnv = "AIMUX_SWARM_OWNED_LEASE_HELPER"
	ownedLeaseFileEnv   = "AIMUX_SWARM_OWNED_LEASE_FILE"
)

type cancellationSignalPipeExecutor struct {
	*aimexecutor.CLIPipeAdapter
	cancelStarted chan struct{}
}

func (e *cancellationSignalPipeExecutor) CancelExecution(_ context.Context, id types.ExecutionID, _ string) (types.CancellationEvidence, error) {
	close(e.cancelStarted)
	return types.CancellationEvidence{ExecutionID: id, NativeAcknowledged: true}, nil
}

type providerCanceledOwnedLeaseExecutor struct{}

func (*providerCanceledOwnedLeaseExecutor) Info() types.ExecutorInfo { return types.ExecutorInfo{} }
func (*providerCanceledOwnedLeaseExecutor) Send(context.Context, types.Message) (*types.Response, error) {
	return &types.Response{ExitCode: 1}, context.Canceled
}

func (*providerCanceledOwnedLeaseExecutor) SendStream(context.Context, types.Message, func(types.Chunk)) (*types.Response, error) {
	return &types.Response{ExitCode: 1}, context.Canceled
}

func (*providerCanceledOwnedLeaseExecutor) SendEvents(context.Context, types.ExecutionID, types.Message, types.ExecutorEventSink) (*types.Response, error) {
	return &types.Response{ExitCode: 1}, context.Canceled
}
func (*providerCanceledOwnedLeaseExecutor) IsAlive() types.HealthStatus { return types.HealthAlive }
func (*providerCanceledOwnedLeaseExecutor) Close() error                { return nil }
func (*providerCanceledOwnedLeaseExecutor) AcquireProcessEvidenceLease(types.ExecutionID) (any, <-chan types.ProcessTreeEvidence, bool) {
	return struct{}{}, nil, true
}

func (*providerCanceledOwnedLeaseExecutor) SendEventsWithProcessEvidenceLease(context.Context, types.ExecutionID, any, types.Message, types.ExecutorEventSink) (*types.Response, error) {
	return &types.Response{ExitCode: 1}, context.Canceled
}
func (*providerCanceledOwnedLeaseExecutor) ReleaseProcessEvidenceLease(types.ExecutionID, any) {}

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

func TestSwarmAlreadyCancelledOwnedLeaseDoesNotStartHelper(t *testing.T) {
	pipe := pipeexecutor.New()
	adapter := aimexecutor.NewCLIPipeAdapter(pipe)
	s := New(func(string) (types.ExecutorV2, error) { return adapter, nil }, nil)
	h, err := s.Get(context.Background(), "already-cancelled", Stateful, WithScope("scope"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	file := t.TempDir() + string(os.PathSeparator) + "started"
	terminal := make(chan types.ExecutorEvent, 1)
	if _, err := s.Execute(ctx, h, "scope", "already-cancelled", ownedLeaseMessage(file), types.ExecutorEventSinkFunc(func(event types.ExecutorEvent) bool {
		if event.Terminal {
			terminal <- event
		}
		return true
	})); !errors.Is(err, context.Canceled) {
		t.Fatalf("already-cancelled Execute = %v, want context.Canceled", err)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatalf("already-cancelled Execute started helper: %v", err)
	}
	select {
	case event := <-terminal:
		if event.Type != "cancelled" {
			t.Fatalf("already-cancelled terminal = %#v, want cancelled", event)
		}
	case <-time.After(time.Second):
		t.Fatal("already-cancelled Execute did not publish one terminal")
	}
	inspection, err := s.Inspect(context.Background(), h, "scope", "already-cancelled")
	if err != nil || !inspection.Terminal || !inspection.Cancelled || inspection.ProcessTreeEvidence.Process.PID != 0 {
		t.Fatalf("already-cancelled inspection = %#v, %v", inspection, err)
	}
	if lease, _, ok := pipe.AcquireProcessEvidenceLease("already-cancelled"); !ok {
		t.Fatal("already-cancelled Execute did not reclaim lease capacity")
	} else {
		pipe.ReleaseProcessEvidenceLease("already-cancelled", lease)
	}
}

func TestSwarmOwnedLeaseProviderCanceledErrorWithoutContextCancellationFails(t *testing.T) {
	exec := &providerCanceledOwnedLeaseExecutor{}
	s := New(func(string) (types.ExecutorV2, error) { return exec, nil }, nil)
	h, err := s.Get(context.Background(), "provider-canceled", Stateful, WithScope("scope"))
	if err != nil {
		t.Fatal(err)
	}
	terminal := make(chan types.ExecutorEvent, 1)
	_, runErr := s.Execute(context.Background(), h, "scope", "provider-canceled", types.Message{}, types.ExecutorEventSinkFunc(func(event types.ExecutorEvent) bool {
		if event.Terminal {
			terminal <- event
		}
		return true
	}))
	if !errors.Is(runErr, context.Canceled) {
		t.Fatalf("Execute = %v, want provider context.Canceled", runErr)
	}
	if event := <-terminal; event.Type != "failed" {
		t.Fatalf("terminal = %#v, want failed", event)
	}
	inspection, err := s.Inspect(context.Background(), h, "scope", "provider-canceled")
	if err != nil || !inspection.Terminal || inspection.Cancelled || inspection.CancellationEvidence != (types.CancellationEvidence{}) {
		t.Fatalf("Inspect = %#v, %v", inspection, err)
	}
}

func TestSwarmOwnedLeaseCancellationAtBoundaryDoesNotStartHelper(t *testing.T) {
	pipe := pipeexecutor.New()
	exec := &cancellationSignalPipeExecutor{
		CLIPipeAdapter: aimexecutor.NewCLIPipeAdapter(pipe),
		cancelStarted:  make(chan struct{}),
	}
	s := New(func(string) (types.ExecutorV2, error) { return exec, nil }, nil)
	h, err := s.Get(context.Background(), "boundary-cancel", Stateful, WithScope("scope"))
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
	file := t.TempDir() + string(os.PathSeparator) + "started"
	terminal := make(chan types.ExecutorEvent, 2)
	executeDone := make(chan error, 1)
	go func() {
		_, err := s.Execute(context.Background(), h, "scope", "boundary-cancel", ownedLeaseMessage(file), types.ExecutorEventSinkFunc(func(event types.ExecutorEvent) bool {
			if event.Terminal {
				terminal <- event
			}
			return true
		}))
		executeDone <- err
	}()
	<-entered
	cancelDone := make(chan error, 1)
	go func() {
		_, err := s.Cancel(context.Background(), h, "scope", "boundary-cancel", "test")
		cancelDone <- err
	}()
	select {
	case <-exec.cancelStarted:
	case <-time.After(time.Second):
		t.Fatal("Swarm Cancel did not cancel the owned lease before release")
	}
	close(release)
	if err := <-executeDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("boundary-cancel Execute = %v, want context.Canceled", err)
	}
	if err := <-cancelDone; err != nil {
		t.Fatalf("boundary-cancel Cancel = %v", err)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatalf("boundary-cancel Execute started helper: %v", err)
	}
	if event := <-terminal; event.Type != "cancelled" {
		t.Fatalf("boundary-cancel terminal = %#v, want cancelled", event)
	}
	select {
	case event := <-terminal:
		t.Fatalf("boundary-cancel published second terminal: %#v", event)
	default:
	}
	inspection, err := s.Inspect(context.Background(), h, "scope", "boundary-cancel")
	if err != nil || !inspection.Terminal || !inspection.Cancelled || inspection.ProcessTreeEvidence.Process.PID != 0 {
		t.Fatalf("boundary-cancel inspection = %#v, %v", inspection, err)
	}
	if lease, _, ok := pipe.AcquireProcessEvidenceLease("boundary-cancel"); !ok {
		t.Fatal("boundary-cancel Execute did not reclaim lease capacity")
	} else {
		pipe.ReleaseProcessEvidenceLease("boundary-cancel", lease)
	}
}
