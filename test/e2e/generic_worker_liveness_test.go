package e2e

import "time"

// waitForT016ProcessExit polls the platform-specific liveness check until
// identity reports dead or timeout elapses. A completed Wait on one process
// in a tree is not proof that the whole tree died; callers must poll every
// captured identity (root, child, grandchild) independently.
func waitForT016ProcessExit(identity *t016ProcessIdentity, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if !t016ProcessAlive(identity) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// t016TreeFixtureSelfExitBound exceeds the known 10s
// "generic-worker --mode tree ... --hold-ms 10000" leaf hold used by every
// T016 lifecycle scenario. A bounded wait for a natural self-exit (used
// where a target cannot safely force-kill a possibly reused PID) must use a
// duration longer than this, so a fixture that is still legitimately
// running is never mistaken for one that simply was not polled long enough.
const t016TreeFixtureSelfExitBound = 15 * time.Second
