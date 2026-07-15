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
