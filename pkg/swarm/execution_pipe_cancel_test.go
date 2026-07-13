package swarm_test

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	aimexecutor "github.com/thebtf/aimux/pkg/executor"
	pipeexecutor "github.com/thebtf/aimux/pkg/executor/pipe"
	"github.com/thebtf/aimux/pkg/swarm"
	"github.com/thebtf/aimux/pkg/types"
)

const (
	pipeDrainHelperEnv     = "AIMUX_SWARM_PIPE_DRAIN_HELPER"
	pipeDrainHelperAddrEnv = "AIMUX_SWARM_PIPE_DRAIN_HELPER_ADDR"
)

type delayedCancellationPipeExecutor struct {
	inner          *aimexecutor.CLIPipeAdapter
	cancelObserved chan struct{}
	releaseInner   chan struct{}
	cancelOnce     sync.Once
}

func (e *delayedCancellationPipeExecutor) Info() types.ExecutorInfo { return e.inner.Info() }
func (e *delayedCancellationPipeExecutor) Send(ctx context.Context, msg types.Message) (*types.Response, error) {
	return e.inner.Send(ctx, msg)
}
func (e *delayedCancellationPipeExecutor) SendStream(ctx context.Context, msg types.Message, onChunk func(types.Chunk)) (*types.Response, error) {
	return e.inner.SendStream(ctx, msg, onChunk)
}
func (e *delayedCancellationPipeExecutor) IsAlive() types.HealthStatus { return e.inner.IsAlive() }
func (e *delayedCancellationPipeExecutor) Close() error                { return e.inner.Close() }

func (e *delayedCancellationPipeExecutor) SendEvents(ctx context.Context, id types.ExecutionID, msg types.Message, sink types.ExecutorEventSink) (*types.Response, error) {
	innerCtx, innerCancel := context.WithCancel(context.Background())
	defer innerCancel()
	type outcome struct {
		response *types.Response
		err      error
	}
	done := make(chan outcome, 1)
	go func() {
		response, err := e.inner.SendEvents(innerCtx, id, msg, sink)
		done <- outcome{response: response, err: err}
	}()

	select {
	case result := <-done:
		return result.response, result.err
	case <-ctx.Done():
		e.cancelOnce.Do(func() { close(e.cancelObserved) })
		<-e.releaseInner
		innerCancel()
		result := <-done
		return result.response, result.err
	}
}

func (e *delayedCancellationPipeExecutor) ProcessTreeEvidence(ctx context.Context, id types.ExecutionID) (types.ProcessTreeEvidence, error) {
	return e.inner.ProcessTreeEvidence(ctx, id)
}

func TestSwarmPipeDrainCancellationHelper(t *testing.T) {
	if os.Getenv(pipeDrainHelperEnv) != "1" {
		return
	}
	conn, err := net.Dial("tcp", os.Getenv(pipeDrainHelperAddrEnv))
	if err != nil {
		t.Fatalf("dial parent: %v", err)
	}
	defer conn.Close()
	if _, err := io.WriteString(os.Stdout, "before-cancel\n"); err != nil {
		t.Fatalf("write initial output: %v", err)
	}
	_ = os.Stdout.Sync()
	if _, err := conn.Write([]byte{1}); err != nil {
		t.Fatalf("ack initial output: %v", err)
	}
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		t.Fatalf("read tail command: %v", err)
	}
	if strings.TrimSpace(line) != "tail" {
		t.Fatalf("tail command = %q", line)
	}
	if _, err := io.WriteString(os.Stdout, "cancellation-tail\n"); err != nil {
		t.Fatalf("write cancellation tail: %v", err)
	}
	_ = os.Stdout.Sync()
	if _, err := conn.Write([]byte{2}); err != nil {
		t.Fatalf("ack cancellation tail: %v", err)
	}
	_, _ = io.Copy(io.Discard, conn)
}

func TestSwarmPipeCancellationDrainsTailBeforeTerminal(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if tcpListener, ok := listener.(*net.TCPListener); ok {
		if err := tcpListener.SetDeadline(time.Now().Add(30 * time.Second)); err != nil {
			t.Fatal(err)
		}
	}

	exec := &delayedCancellationPipeExecutor{
		inner:          aimexecutor.NewCLIPipeAdapter(pipeexecutor.New()),
		cancelObserved: make(chan struct{}),
		releaseInner:   make(chan struct{}),
	}
	s := swarm.New(func(string) (types.ExecutorV2, error) { return exec, nil }, nil, swarm.WithStatefulTTL(0))
	const scope = "scope-a"
	h, err := s.Get(context.Background(), "pipe", swarm.Stateful, swarm.WithScope(scope))
	if err != nil {
		t.Fatal(err)
	}

	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(exec.releaseInner) }) }
	runCtx, cancelRun := context.WithCancel(context.Background())
	defer func() {
		cancelRun()
		release()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		_ = s.Shutdown(shutdownCtx)
	}()

	var mu sync.Mutex
	var events []types.ExecutorEvent
	beforeSeen := make(chan struct{})
	tailSeen := make(chan struct{})
	terminalSeen := make(chan struct{}, 1)
	var beforeOnce sync.Once
	var tailOnce sync.Once
	type runOutcome struct {
		response *types.Response
		err      error
	}
	runDone := make(chan runOutcome, 1)
	go func() {
		response, runErr := s.Execute(runCtx, h, scope, "pipe-cancel-tail", types.Message{Spawn: &types.SpawnArgs{
			Command: os.Args[0],
			Args: []string{
				"-test.run=^TestSwarmPipeDrainCancellationHelper$",
				"-test.count=1",
			},
			Env: map[string]string{
				pipeDrainHelperEnv:     "1",
				pipeDrainHelperAddrEnv: listener.Addr().String(),
			},
		}}, types.ExecutorEventSinkFunc(func(event types.ExecutorEvent) bool {
			mu.Lock()
			events = append(events, event)
			mu.Unlock()
			if event.Terminal {
				terminalSeen <- struct{}{}
				return true
			}
			if bytes.Contains(event.Content, []byte("before-cancel")) {
				beforeOnce.Do(func() { close(beforeSeen) })
			}
			if bytes.Contains(event.Content, []byte("cancellation-tail")) {
				tailOnce.Do(func() { close(tailSeen) })
				return false
			}
			return true
		}))
		runDone <- runOutcome{response: response, err: runErr}
	}()

	conn, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(30 * time.Second)); err != nil {
		t.Fatal(err)
	}
	ack := []byte{0}
	if _, err := io.ReadFull(conn, ack); err != nil || ack[0] != 1 {
		t.Fatalf("initial ack = %v, %v", ack, err)
	}
	select {
	case <-beforeSeen:
	case <-time.After(30 * time.Second):
		t.Fatal("initial pipe output was not admitted")
	}

	cancelDone := make(chan struct {
		evidence types.CancellationEvidence
		err      error
	}, 1)
	go func() {
		evidence, cancelErr := s.Cancel(context.Background(), h, scope, "pipe-cancel-tail", "test")
		cancelDone <- struct {
			evidence types.CancellationEvidence
			err      error
		}{evidence, cancelErr}
	}()
	select {
	case <-exec.cancelObserved:
	case <-time.After(5 * time.Second):
		t.Fatal("pipe wrapper did not observe cancellation")
	}
	select {
	case <-terminalSeen:
		t.Fatal("terminal was published before the real pipe drain completed")
	default:
	}
	if _, err := fmt.Fprintln(conn, "tail"); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(conn, ack); err != nil || ack[0] != 2 {
		t.Fatalf("tail ack = %v, %v", ack, err)
	}
	select {
	case <-tailSeen:
	case <-time.After(30 * time.Second):
		t.Fatal("cancellation tail was not drained through the real pipe executor")
	}
	select {
	case <-terminalSeen:
		t.Fatal("terminal was published before the rejected pipe tail was accounted for")
	default:
	}

	release()
	cancelResult := <-cancelDone
	if cancelResult.err != nil || cancelResult.evidence.NativeAcknowledged {
		t.Fatalf("Cancel = %#v, %v, want native false", cancelResult.evidence, cancelResult.err)
	}
	var result runOutcome
	select {
	case result = <-runDone:
	case <-time.After(30 * time.Second):
		t.Fatal("execution did not finish after releasing pipe cancellation")
	}
	if result.err != nil {
		t.Fatal(result.err)
	}
	if result.response == nil || !result.response.Partial {
		t.Fatalf("response = %#v, want Partial after rejected pipe tail", result.response)
	}
	inspection, inspectErr := s.Inspect(context.Background(), h, scope, "pipe-cancel-tail")
	if inspectErr != nil || !inspection.Cancelled || !inspection.ProcessTreeEvidence.Stopped || inspection.ProcessTreeEvidence.Process.Validate() != nil {
		t.Fatalf("Inspect = %#v, %v, want stopped exact process proof", inspection, inspectErr)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(events) < 3 {
		t.Fatalf("events = %#v, want initial output, tail decision, and terminal", events)
	}
	terminal := events[len(events)-1]
	if !terminal.Terminal || terminal.Type != "cancelled" || !terminal.Truncated {
		t.Fatalf("last event = %#v, want cancelled truncated terminal", terminal)
	}
}
