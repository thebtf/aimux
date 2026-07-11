//go:build windows

package executor

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sys/windows"
)

const (
	primaryThreadBatchSize   = 64
	primaryThreadQueueSize   = 256
	primaryThreadBatchWindow = 2 * time.Millisecond
)

var (
	errPrimaryThreadBatcherClosed = errors.New("primary thread batcher is closed")
	errPrimaryThreadBatcherFull   = errors.New("primary thread batcher queue is full")

	processPrimaryThreadBatcher = newPrimaryThreadBatcher(
		primaryThreadBatchSize,
		primaryThreadQueueSize,
		primaryThreadBatchWindow,
		snapshotThreadEntries,
		resumeThread,
	)
)

type primaryThreadRequest struct {
	processID uint32
	result    chan error
}

type primaryThreadBatcher struct {
	maxBatch    int
	batchWindow time.Duration
	requests    chan primaryThreadRequest
	stop        chan struct{}
	stopped     chan struct{}
	enqueueMu   sync.Mutex
	closeOnce   sync.Once
	closed      atomic.Bool
	snapshot    func() ([]windows.ThreadEntry32, error)
	resume      func(uint32) error
}

func newPrimaryThreadBatcher(
	maxBatch int,
	maxPending int,
	batchWindow time.Duration,
	snapshot func() ([]windows.ThreadEntry32, error),
	resume func(uint32) error,
) *primaryThreadBatcher {
	if maxBatch <= 0 {
		panic("primary thread batch size must be positive")
	}
	if maxPending <= 0 {
		panic("primary thread queue size must be positive")
	}
	if batchWindow < 0 {
		panic("primary thread batch window cannot be negative")
	}
	if snapshot == nil {
		panic("primary thread snapshot function is required")
	}
	if resume == nil {
		panic("primary thread resume function is required")
	}

	batcher := &primaryThreadBatcher{
		maxBatch:    maxBatch,
		batchWindow: batchWindow,
		requests:    make(chan primaryThreadRequest, maxPending),
		stop:        make(chan struct{}),
		stopped:     make(chan struct{}),
		snapshot:    snapshot,
		resume:      resume,
	}
	go batcher.run()
	return batcher
}

func (batcher *primaryThreadBatcher) resumeProcess(processID uint32) error {
	batcher.enqueueMu.Lock()
	if batcher.closed.Load() {
		batcher.enqueueMu.Unlock()
		return errPrimaryThreadBatcherClosed
	}
	request := primaryThreadRequest{
		processID: processID,
		result:    make(chan error, 1),
	}
	select {
	case <-batcher.stop:
		batcher.enqueueMu.Unlock()
		return errPrimaryThreadBatcherClosed
	case batcher.requests <- request:
		batcher.enqueueMu.Unlock()
	default:
		batcher.enqueueMu.Unlock()
		return fmt.Errorf(
			"%w (capacity %d)",
			errPrimaryThreadBatcherFull,
			cap(batcher.requests),
		)
	}

	select {
	case err := <-request.result:
		return err
	case <-batcher.stop:
		return errPrimaryThreadBatcherClosed
	}
}

func (batcher *primaryThreadBatcher) run() {
	defer close(batcher.stopped)
	for {
		select {
		case <-batcher.stop:
			batcher.rejectQueued(errPrimaryThreadBatcherClosed)
			return
		case first := <-batcher.requests:
			batch := []primaryThreadRequest{first}
			timer := time.NewTimer(batcher.batchWindow)
			collecting := true
			for collecting && len(batch) < batcher.maxBatch {
				select {
				case <-batcher.stop:
					stopAndDrainTimer(timer)
					batcher.rejectBatch(batch, errPrimaryThreadBatcherClosed)
					batcher.rejectQueued(errPrimaryThreadBatcherClosed)
					return
				case request := <-batcher.requests:
					batch = append(batch, request)
				case <-timer.C:
					collecting = false
				}
			}
			stopAndDrainTimer(timer)

			if batcher.closed.Load() {
				batcher.rejectBatch(batch, errPrimaryThreadBatcherClosed)
				batcher.rejectQueued(errPrimaryThreadBatcherClosed)
				return
			}
			batcher.resolveBatch(batch)
		}
	}
}

func (batcher *primaryThreadBatcher) resolveBatch(batch []primaryThreadRequest) {
	entries, err := batcher.snapshot()
	if err != nil {
		batcher.rejectBatch(batch, err)
		return
	}
	for _, request := range batch {
		if batcher.closed.Load() {
			request.result <- errPrimaryThreadBatcherClosed
			continue
		}
		threadID, selectErr := selectSoleProcessThread(request.processID, entries)
		if selectErr != nil {
			request.result <- selectErr
			continue
		}
		request.result <- batcher.resume(threadID)
	}
}

func (batcher *primaryThreadBatcher) rejectBatch(batch []primaryThreadRequest, err error) {
	for _, request := range batch {
		request.result <- err
	}
}

func (batcher *primaryThreadBatcher) rejectQueued(err error) {
	for {
		select {
		case request := <-batcher.requests:
			request.result <- err
		default:
			return
		}
	}
}

func (batcher *primaryThreadBatcher) Close() {
	batcher.closeOnce.Do(func() {
		batcher.enqueueMu.Lock()
		batcher.closed.Store(true)
		close(batcher.stop)
		batcher.enqueueMu.Unlock()
		<-batcher.stopped
	})
}

func stopAndDrainTimer(timer *time.Timer) {
	if timer.Stop() {
		return
	}
	select {
	case <-timer.C:
	default:
	}
}
