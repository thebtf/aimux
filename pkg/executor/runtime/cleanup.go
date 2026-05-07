package runtime

import (
	"errors"
	"fmt"
	"log"
	"os"
)

// EphemeralCleanupHook is a PostExitHook that removes the VirtualHomeDir when
// StateScope is StateScopeEphemeral. It is idempotent: if the directory has
// already been removed, it returns nil.
//
// Removal failures are logged as warnings but do not return an error — cleanup
// is best-effort. Callers should not rely on the directory being present after
// this hook runs, regardless of the return value.
//
// Register with ProfileBuilder.WithPostExitHook:
//
//	profile := From(IsolatedCodexProfile(workDir)).
//	    WithPostExitHook(EphemeralCleanupHook).
//	    Build()
func EphemeralCleanupHook(profile CLIRuntimeProfile, _ int) error {
	if profile.StateScope != StateScopeEphemeral {
		return nil
	}
	if profile.VirtualHomeDir == "" {
		return nil
	}
	if err := os.RemoveAll(profile.VirtualHomeDir); err != nil {
		// os.RemoveAll returns nil when the path does not exist, so any error
		// here indicates a genuine removal failure (e.g., permission denied).
		if !errors.Is(err, os.ErrNotExist) {
			log.Printf("runtime: EphemeralCleanupHook: failed to remove %q: %v", profile.VirtualHomeDir, err)
		}
	}
	return nil
}

// RunPostExitHooks runs all PostExitHooks on the profile in order, passing exitCode.
// Errors from individual hooks are collected and returned as a combined error.
// Hook execution continues even after a failure — all hooks run regardless.
func RunPostExitHooks(profile CLIRuntimeProfile, exitCode int) error {
	var errs []error
	for i, hook := range profile.PostExitHooks {
		if hook == nil {
			continue
		}
		if err := hook(profile, exitCode); err != nil {
			errs = append(errs, fmt.Errorf("PostExitHook[%d]: %w", i, err))
		}
	}
	return errors.Join(errs...)
}
