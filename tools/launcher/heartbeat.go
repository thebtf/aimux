// Package main — heartbeat.go provides the idle-heartbeat goroutine for --diag mode.
//
// startHeartbeat launches a background goroutine that emits KindHeartbeat events
// and stderr log lines whenever the monitored process has produced no output for
// heartbeatInterval seconds.  Callers close the returned stop channel when the
// process exits to terminate the goroutine cleanly.
package main

import (
	"fmt"
	"os"
	"sync/atomic"
	"time"
)

const heartbeatInterval = 5 * time.Second

// heartbeatState is shared between the diag OnOutput callback and the heartbeat
// goroutine.  lastOutputNano is updated atomically so no mutex is needed on the
// hot path (every output line).
type heartbeatState struct {
	// lastOutputNano is the Unix nanosecond timestamp of the most recent output
	// line received via the OnOutput callback.  Updated with atomic store.
	lastOutputNano atomic.Int64

	// startTime is the wall-clock time when the Run call began.
	startTime time.Time
}

// newHeartbeatState initialises a heartbeatState with startTime = now and
// lastOutputNano = now (so the heartbeat goroutine does not immediately fire).
func newHeartbeatState(start time.Time) *heartbeatState {
	hs := &heartbeatState{startTime: start}
	hs.lastOutputNano.Store(start.UnixNano())
	return hs
}

// touch records the current time as the last output time.  Called from the
// OnOutput callback on each new line.
func (hs *heartbeatState) touch() {
	hs.lastOutputNano.Store(time.Now().UnixNano())
}

// startHeartbeat launches the heartbeat goroutine.  Returns a stop channel:
// the caller must close it when the process terminates.
//
//   - sink  — event sink for KindHeartbeat events
//   - hs    — shared idle state (updated by touch())
func startHeartbeat(sink EventSink, hs *heartbeatState) (stop chan struct{}) {
	stop = make(chan struct{})

	go func() {
		// Poll at 1s resolution so the heartbeat fires within 1s of the deadline
		// (lastOutput + heartbeatInterval).  A ticker at heartbeatInterval itself
		// could delay the first event by up to 2×heartbeatInterval in the worst case
		// (CodeRabbit PRRT_kwDOR6K9ds5_EJ03).
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-stop:
				return
			case t := <-ticker.C:
				lastNano := hs.lastOutputNano.Load()
				idleSince := time.Unix(0, lastNano)
				deadline := idleSince.Add(heartbeatInterval)

				// Skip until the deadline has been reached.
				if t.Before(deadline) {
					continue
				}

				idleSeconds := t.Sub(idleSince).Seconds()

				totalElapsed := t.Sub(hs.startTime).Seconds()

				fmt.Fprintf(
					os.Stderr,
					"[+%.1fs] ...still waiting (no output for %.1fs)\n",
					totalElapsed,
					idleSeconds,
				)

				sink.Emit(KindHeartbeat, heartbeatPayload{
					IdleSeconds:  idleSeconds,
					TotalElapsed: totalElapsed,
				})
			}
		}
	}()

	return stop
}
