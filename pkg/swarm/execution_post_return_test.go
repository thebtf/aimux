package swarm

import (
	"context"
	"testing"
	"time"

	"github.com/thebtf/aimux/pkg/types"
)

type postReturnSuccessExecutor struct{ returned chan struct{} }

func (*postReturnSuccessExecutor) Info() types.ExecutorInfo { return types.ExecutorInfo{} }
func (*postReturnSuccessExecutor) Send(context.Context, types.Message) (*types.Response, error) {
	return &types.Response{}, nil
}
func (*postReturnSuccessExecutor) SendStream(context.Context, types.Message, func(types.Chunk)) (*types.Response, error) {
	return &types.Response{}, nil
}
func (*postReturnSuccessExecutor) IsAlive() types.HealthStatus { return types.HealthAlive }
func (*postReturnSuccessExecutor) Close() error                { return nil }
func (e *postReturnSuccessExecutor) SendEvents(context.Context, types.ExecutionID, types.Message, types.ExecutorEventSink) (*types.Response, error) {
	close(e.returned)
	return &types.Response{}, nil
}
func (*postReturnSuccessExecutor) CancelExecution(_ context.Context, id types.ExecutionID, _ string) (types.CancellationEvidence, error) {
	return types.CancellationEvidence{ExecutionID: id, NativeAcknowledged: true}, nil
}

func TestSwarmPostReturnCancelCannotReclassifyNaturalSuccess(t *testing.T) {
	exec := &postReturnSuccessExecutor{returned: make(chan struct{})}
	s := New(func(string) (types.ExecutorV2, error) { return exec, nil }, nil)
	h, err := s.Get(context.Background(), "post-return", Stateful, WithScope("scope"))
	if err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	s.postExecutorReturn = func() {
		close(entered)
		<-release
	}
	terminal := make(chan types.ExecutorEvent, 1)
	runDone := make(chan error, 1)
	go func() {
		_, runErr := s.Execute(context.Background(), h, "scope", "post-return", types.Message{}, types.ExecutorEventSinkFunc(func(event types.ExecutorEvent) bool {
			if event.Terminal {
				terminal <- event
			}
			return true
		}))
		runDone <- runErr
	}()
	<-exec.returned
	<-entered
	cancelDone := make(chan error, 1)
	go func() {
		_, cancelErr := s.Cancel(context.Background(), h, "scope", "post-return", "after successful return")
		cancelDone <- cancelErr
	}()
	select {
	case cancelErr := <-cancelDone:
		if cancelErr != nil {
			t.Fatal(cancelErr)
		}
	case <-time.After(time.Second):
		t.Fatal("Cancel did not resolve native acknowledgement")
	}
	close(release)
	if runErr := <-runDone; runErr != nil {
		t.Fatal(runErr)
	}
	if event := <-terminal; event.Type != "completed" {
		t.Fatalf("terminal = %#v, want completed", event)
	}
	inspection, err := s.Inspect(context.Background(), h, "scope", "post-return")
	if err != nil || !inspection.Terminal || inspection.Cancelled {
		t.Fatalf("Inspect = %#v, %v", inspection, err)
	}
}
