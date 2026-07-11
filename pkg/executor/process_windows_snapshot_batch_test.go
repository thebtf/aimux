//go:build windows

package executor

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestPrimaryThreadBatcher_ResolveBatchUsesOneSnapshot(t *testing.T) {
	const cohortSize = 8
	entries := make([]windows.ThreadEntry32, 0, cohortSize)
	for i := range cohortSize {
		entries = append(entries, windows.ThreadEntry32{
			ThreadID:       uint32(1000 + i),
			OwnerProcessID: uint32(2000 + i),
		})
	}

	var snapshotCalls atomic.Int32
	var resumedMu sync.Mutex
	resumed := make(map[uint32]int, cohortSize)
	batcher := newPrimaryThreadBatcher(
		cohortSize,
		cohortSize,
		time.Millisecond,
		func() ([]windows.ThreadEntry32, error) {
			snapshotCalls.Add(1)
			return entries, nil
		},
		func(threadID uint32) error {
			resumedMu.Lock()
			defer resumedMu.Unlock()
			resumed[threadID]++
			return nil
		},
	)
	defer batcher.Close()

	requests := make([]primaryThreadRequest, 0, cohortSize)
	for i := range cohortSize {
		requests = append(requests, primaryThreadRequest{
			processID: uint32(2000 + i),
			result:    make(chan error, 1),
		})
	}
	batcher.resolveBatch(requests)

	if got := snapshotCalls.Load(); got != 1 {
		t.Fatalf("snapshot calls = %d, want 1 for one cohort", got)
	}
	for i, request := range requests {
		if err := <-request.result; err != nil {
			t.Fatalf("request %d failed: %v", i, err)
		}
		threadID := uint32(1000 + i)
		if resumed[threadID] != 1 {
			t.Errorf("thread %d resumed %d times, want exactly once", threadID, resumed[threadID])
		}
	}
}

func TestPrimaryThreadBatcher_ConcurrentRequestsCoalesce(t *testing.T) {
	const cohortSize = 8
	entries := make([]windows.ThreadEntry32, 0, cohortSize)
	for i := range cohortSize {
		entries = append(entries, windows.ThreadEntry32{
			ThreadID:       uint32(5000 + i),
			OwnerProcessID: uint32(6000 + i),
		})
	}

	var snapshotCalls atomic.Int32
	var resumeCalls atomic.Int32
	batcher := newPrimaryThreadBatcher(
		cohortSize,
		cohortSize,
		250*time.Millisecond,
		func() ([]windows.ThreadEntry32, error) {
			snapshotCalls.Add(1)
			return entries, nil
		},
		func(uint32) error {
			resumeCalls.Add(1)
			return nil
		},
	)
	defer batcher.Close()

	startGate := make(chan struct{})
	results := make(chan error, cohortSize)
	var ready sync.WaitGroup
	ready.Add(cohortSize)
	for i := range cohortSize {
		go func(index int) {
			ready.Done()
			<-startGate
			results <- batcher.resumeProcess(uint32(6000 + index))
		}(i)
	}
	ready.Wait()
	close(startGate)

	for i := range cohortSize {
		select {
		case err := <-results:
			if err != nil {
				t.Fatalf("concurrent request %d failed: %v", i, err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("concurrent request did not complete")
		}
	}
	if got := snapshotCalls.Load(); got != 1 {
		t.Fatalf("snapshot calls = %d, want 1 for an eight-request cohort", got)
	}
	if got := resumeCalls.Load(); got != cohortSize {
		t.Fatalf("resume calls = %d, want %d", got, cohortSize)
	}
}

func TestPrimaryThreadBatcher_PreservesPerProcessExactOne(t *testing.T) {
	entries := []windows.ThreadEntry32{
		{ThreadID: 7000, OwnerProcessID: 8000},
		{ThreadID: 7001, OwnerProcessID: 8000},
		{ThreadID: 7002, OwnerProcessID: 8002},
	}
	var resumed atomic.Uint32
	batcher := newPrimaryThreadBatcher(
		3,
		3,
		time.Millisecond,
		func() ([]windows.ThreadEntry32, error) { return entries, nil },
		func(threadID uint32) error {
			resumed.Store(threadID)
			return nil
		},
	)
	defer batcher.Close()

	requests := []primaryThreadRequest{
		{processID: 8000, result: make(chan error, 1)},
		{processID: 8001, result: make(chan error, 1)},
		{processID: 8002, result: make(chan error, 1)},
	}
	batcher.resolveBatch(requests)

	if err := <-requests[0].result; err == nil || err.Error() != "process 8000 thread snapshot found 2 candidates; expected exactly one" {
		t.Fatalf("duplicate-thread error = %v", err)
	}
	if err := <-requests[1].result; err == nil || err.Error() != "process 8001 thread snapshot found 0 candidates; expected exactly one" {
		t.Fatalf("missing-thread error = %v", err)
	}
	if err := <-requests[2].result; err != nil {
		t.Fatalf("valid exact-one request failed: %v", err)
	}
	if got := resumed.Load(); got != 7002 {
		t.Fatalf("resumed thread = %d, want 7002", got)
	}
}

func TestPrimaryThreadBatcher_SnapshotErrorReleasesEveryRequest(t *testing.T) {
	const cohortSize = 8
	wantErr := errors.New("snapshot failed")
	var snapshotCalls atomic.Int32
	batcher := newPrimaryThreadBatcher(
		cohortSize,
		cohortSize,
		time.Millisecond,
		func() ([]windows.ThreadEntry32, error) {
			snapshotCalls.Add(1)
			return nil, wantErr
		},
		func(uint32) error {
			t.Fatal("resume called after snapshot failure")
			return nil
		},
	)
	defer batcher.Close()

	requests := make([]primaryThreadRequest, 0, cohortSize)
	for i := range cohortSize {
		requests = append(requests, primaryThreadRequest{
			processID: uint32(3000 + i),
			result:    make(chan error, 1),
		})
	}
	batcher.resolveBatch(requests)

	if got := snapshotCalls.Load(); got != 1 {
		t.Fatalf("snapshot calls = %d, want 1 for failed cohort", got)
	}
	for i, request := range requests {
		if err := <-request.result; !errors.Is(err, wantErr) {
			t.Errorf("request %d error = %v, want %v", i, err, wantErr)
		}
	}
}

func TestPrimaryThreadBatcher_QueueIsHardBounded(t *testing.T) {
	snapshotStarted := make(chan struct{})
	releaseSnapshot := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseSnapshot) }) }
	var snapshotCalls atomic.Int32
	batcher := newPrimaryThreadBatcher(
		1,
		1,
		0,
		func() ([]windows.ThreadEntry32, error) {
			if snapshotCalls.Add(1) == 1 {
				close(snapshotStarted)
				<-releaseSnapshot
			}
			return []windows.ThreadEntry32{
				{ThreadID: 9000, OwnerProcessID: 9100},
				{ThreadID: 9001, OwnerProcessID: 9101},
			}, nil
		},
		func(uint32) error { return nil },
	)
	defer func() {
		release()
		batcher.Close()
	}()

	firstResult := make(chan error, 1)
	go func() { firstResult <- batcher.resumeProcess(9100) }()
	select {
	case <-snapshotStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("first request did not enter snapshot")
	}

	secondResult := make(chan error, 1)
	go func() { secondResult <- batcher.resumeProcess(9101) }()
	deadline := time.Now().Add(5 * time.Second)
	for len(batcher.requests) != 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := len(batcher.requests); got != 1 {
		t.Fatalf("queued requests = %d, want one request filling the bounded queue", got)
	}
	if err := batcher.resumeProcess(9102); !errors.Is(err, errPrimaryThreadBatcherFull) {
		t.Fatalf("overflow error = %v, want queue-full error", err)
	}

	release()
	for i, result := range []chan error{firstResult, secondResult} {
		select {
		case err := <-result:
			if err != nil {
				t.Fatalf("accepted request %d failed: %v", i, err)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("accepted request %d did not complete", i)
		}
	}
}

func TestPrimaryThreadBatcher_CloseReleasesQueuedWaiters(t *testing.T) {
	const cohortSize = 4
	snapshotStarted := make(chan struct{})
	releaseSnapshot := make(chan struct{})
	var signalSnapshot sync.Once
	batcher := newPrimaryThreadBatcher(
		cohortSize,
		cohortSize,
		time.Millisecond,
		func() ([]windows.ThreadEntry32, error) {
			signalSnapshot.Do(func() { close(snapshotStarted) })
			<-releaseSnapshot
			return nil, nil
		},
		func(uint32) error {
			t.Fatal("resume called while batcher is closing")
			return nil
		},
	)

	errorsCh := make(chan error, cohortSize)
	startGate := make(chan struct{})
	for i := range cohortSize {
		go func(index int) {
			<-startGate
			errorsCh <- batcher.resumeProcess(uint32(4000 + index))
		}(i)
	}
	close(startGate)

	select {
	case <-snapshotStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("batcher did not begin the queued snapshot")
	}
	closed := make(chan struct{})
	go func() {
		batcher.Close()
		close(closed)
	}()
	for range cohortSize {
		select {
		case err := <-errorsCh:
			if !errors.Is(err, errPrimaryThreadBatcherClosed) {
				t.Errorf("resumeProcess error = %v, want closed error", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("queued waiter was not released by Close")
		}
	}
	close(releaseSnapshot)
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("batcher Close did not finish after the snapshot returned")
	}
	if got := len(batcher.requests); got != 0 {
		t.Fatalf("requests retained after Close = %d, want 0", got)
	}
}
