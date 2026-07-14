package swarm

import (
	"context"
	"errors"
	"testing"

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
	joinCtx, joinCancel := context.WithCancel(context.Background())
	joinCancel()
	if _, cancelErr := s.Cancel(joinCtx, h, "scope", "post-return", "after successful return"); cancelErr != context.Canceled {
		t.Fatalf("post-return Cancel = %v, want context cancellation while terminal is unpublished", cancelErr)
	}
	if _, inspectErr := s.Inspect(joinCtx, h, "scope", "post-return"); inspectErr != context.Canceled {
		t.Fatalf("post-return Inspect = %v, want context cancellation while terminal is unpublished", inspectErr)
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

func TestSwarmPreOutcomeCancelCannotReclassifyNaturalSuccess(t *testing.T) {
	exec := &postReturnSuccessExecutor{returned: make(chan struct{})}
	s := New(func(string) (types.ExecutorV2, error) { return exec, nil }, nil)
	h, err := s.Get(context.Background(), "pre-outcome", Stateful, WithScope("scope"))
	if err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	s.beforeOutcomeCapture = func() {
		close(entered)
		<-release
	}
	terminal := make(chan types.ExecutorEvent, 1)
	runDone := make(chan error, 1)
	go func() {
		_, runErr := s.Execute(context.Background(), h, "scope", "pre-outcome", types.Message{}, types.ExecutorEventSinkFunc(func(event types.ExecutorEvent) bool {
			if event.Terminal {
				terminal <- event
			}
			return true
		}))
		runDone <- runErr
	}()
	<-exec.returned
	<-entered
	evidence, cancelErr := s.Cancel(context.Background(), h, "scope", "pre-outcome", "after successful return")
	if cancelErr != nil || !evidence.NativeAcknowledged {
		t.Fatalf("pre-outcome Cancel = %#v, %v", evidence, cancelErr)
	}
	close(release)
	if runErr := <-runDone; runErr != nil {
		t.Fatal(runErr)
	}
	if event := <-terminal; event.Type != "completed" {
		t.Fatalf("terminal = %#v, want completed", event)
	}
	inspection, err := s.Inspect(context.Background(), h, "scope", "pre-outcome")
	if err != nil || !inspection.Terminal || inspection.Cancelled {
		t.Fatalf("Inspect = %#v, %v", inspection, err)
	}
}

type postReturnFailureExecutor struct {
	returned, cancellationStarted chan struct{}
}

func (*postReturnFailureExecutor) Info() types.ExecutorInfo { return types.ExecutorInfo{} }
func (*postReturnFailureExecutor) Send(context.Context, types.Message) (*types.Response, error) {
	return &types.Response{ExitCode: 1}, nil
}

func (*postReturnFailureExecutor) SendStream(context.Context, types.Message, func(types.Chunk)) (*types.Response, error) {
	return &types.Response{ExitCode: 1}, nil
}
func (*postReturnFailureExecutor) IsAlive() types.HealthStatus { return types.HealthAlive }
func (*postReturnFailureExecutor) Close() error                { return nil }
func (e *postReturnFailureExecutor) SendEvents(context.Context, types.ExecutionID, types.Message, types.ExecutorEventSink) (*types.Response, error) {
	close(e.returned)
	return &types.Response{ExitCode: 1, Error: types.NewExecutorError("natural cancellation-shaped failure", context.Canceled, "")}, nil
}

func (e *postReturnFailureExecutor) CancelExecution(_ context.Context, id types.ExecutionID, _ string) (types.CancellationEvidence, error) {
	close(e.cancellationStarted)
	return types.CancellationEvidence{ExecutionID: id, NativeAcknowledged: true}, nil
}

func TestSwarmPostReturnNaturalFailureCannotBeRelabelledCancelled(t *testing.T) {
	exec := &postReturnFailureExecutor{returned: make(chan struct{}), cancellationStarted: make(chan struct{})}
	s := New(func(string) (types.ExecutorV2, error) { return exec, nil }, nil)
	h, err := s.Get(context.Background(), "post-return-failure", Stateful, WithScope("scope"))
	if err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	s.beforeOutcomeCapture = func() {
		close(entered)
		<-release
	}
	terminal := make(chan types.ExecutorEvent, 1)
	runDone := make(chan error, 1)
	go func() {
		_, runErr := s.Execute(context.Background(), h, "scope", "post-return-failure", types.Message{}, types.ExecutorEventSinkFunc(func(event types.ExecutorEvent) bool {
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
		_, cancelErr := s.Cancel(context.Background(), h, "scope", "post-return-failure", "after natural failure")
		cancelDone <- cancelErr
	}()
	<-exec.cancellationStarted
	close(release)
	if cancelErr := <-cancelDone; cancelErr != nil {
		t.Fatal(cancelErr)
	}
	if runErr := <-runDone; runErr != nil {
		t.Fatal(runErr)
	}
	if event := <-terminal; event.Type != "failed" {
		t.Fatalf("terminal = %#v, want failed", event)
	}
	inspection, err := s.Inspect(context.Background(), h, "scope", "post-return-failure")
	if err != nil || !inspection.Terminal || inspection.Cancelled {
		t.Fatalf("Inspect = %#v, %v", inspection, err)
	}
}

func TestClassifyExecutionOutcomeKeepsNaturalErrorsFailed(t *testing.T) {
	for _, test := range []struct {
		name     string
		response *types.Response
		err      error
	}{
		{name: "non-zero", response: &types.Response{ExitCode: 1}},
		{name: "returned error", response: &types.Response{}, err: errors.New("natural failure")},
		{name: "response error", response: &types.Response{Error: types.NewExecutorError("natural failure", nil, "")}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := classifyExecutionOutcome(test.response, test.err, true); got != executionOutcomeFailed {
				t.Fatalf("classifyExecutionOutcome = %d, want natural failure", got)
			}
		})
	}
}
