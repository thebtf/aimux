package codex

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sync"
	"time"

	"github.com/thebtf/aimux/pkg/executor/runtime"
)

// PoolConfig holds configuration for CodexPool.
type PoolConfig struct {
	// IdleTimeout is how long an idle AppServerProcess is kept before shutdown.
	// Zero disables idle eviction.
	IdleTimeout time.Duration

	// DefaultProfile is used when no per-project profile is provided.
	DefaultProfile func(workDir string) runtime.CLIRuntimeProfile
}

// DefaultPoolConfig returns a production-ready PoolConfig.
func DefaultPoolConfig() PoolConfig {
	return PoolConfig{
		IdleTimeout:    5 * time.Minute,
		DefaultProfile: runtime.DefaultCodexProfile,
	}
}

// poolEntry wraps an AppServerProcess with idle tracking.
type poolEntry struct {
	process  *AppServerProcess
	lastUsed time.Time
}

// CodexPool maintains one AppServerProcess per project ID.
// The project ID is the aimux ProjectContext.ID (hash of worktree root).
//
// The pool owns the lifecycle of each process:
//   - Acquire creates or returns an existing ready process.
//   - Release marks a process as no longer in active use (idle timer starts).
//   - Shutdown tears down all processes gracefully.
//
// Thread-safe.
type CodexPool struct {
	cfg       PoolConfig
	codexPath string

	mu      sync.Mutex
	entries map[string]*poolEntry // projectID → poolEntry

	stopOnce sync.Once
	stopCh   chan struct{}
	wg       sync.WaitGroup
}

// NewCodexPool constructs a CodexPool.
// codexPath must be the absolute path to the codex binary.
// Returns an error if codexPath is empty or the binary is not executable.
func NewCodexPool(codexPath string, cfg PoolConfig) (*CodexPool, error) {
	if codexPath == "" {
		return nil, errors.New("codex: CodexPool: codexPath must not be empty")
	}
	// Verify the binary exists and is executable.
	if _, err := exec.LookPath(codexPath); err != nil {
		return nil, fmt.Errorf("codex: CodexPool: codex binary not found at %q: %w", codexPath, err)
	}
	if cfg.DefaultProfile == nil {
		cfg.DefaultProfile = runtime.DefaultCodexProfile
	}
	p := &CodexPool{
		cfg:       cfg,
		codexPath: codexPath,
		entries:   make(map[string]*poolEntry),
		stopCh:    make(chan struct{}),
	}
	if cfg.IdleTimeout > 0 {
		p.wg.Add(1)
		go p.idleEvictLoop()
	}
	return p, nil
}

// Acquire returns a started AppServerProcess for the given project.
// If no process exists for the project, one is spawned and initialized.
// The workDir parameter is used to build the default profile when no
// existing entry is found.
//
// If codex is not installed, Acquire returns an actionable error.
func (p *CodexPool) Acquire(ctx context.Context, projectID, workDir string) (*AppServerProcess, error) {
	if projectID == "" {
		return nil, errors.New("codex: CodexPool.Acquire: projectID must not be empty")
	}

	p.mu.Lock()
	entry, ok := p.entries[projectID]
	if ok {
		entry.lastUsed = time.Now()
		proc := entry.process
		p.mu.Unlock()
		return proc, nil
	}

	// Build profile and create process while lock is held to prevent double-spawn.
	profile := p.cfg.DefaultProfile(workDir)
	proc := NewAppServerProcess(p.codexPath, profile)
	p.entries[projectID] = &poolEntry{
		process:  proc,
		lastUsed: time.Now(),
	}
	p.mu.Unlock()

	// Start the process outside the lock (I/O and RPC).
	if err := proc.Start(ctx); err != nil {
		// Remove the failed entry so the next Acquire can retry.
		p.mu.Lock()
		delete(p.entries, projectID)
		p.mu.Unlock()
		return nil, fmt.Errorf("codex: CodexPool.Acquire: start process for project %q: %w", projectID, err)
	}

	return proc, nil
}

// Release updates the idle timestamp for a project's process.
// It does not stop the process — idle eviction handles that.
// Callers MUST call Release after each Acquire to keep the idle timer accurate.
func (p *CodexPool) Release(projectID string) {
	p.mu.Lock()
	if entry, ok := p.entries[projectID]; ok {
		entry.lastUsed = time.Now()
	}
	p.mu.Unlock()
}

// Remove shuts down and removes the process for a project.
// It is a no-op if no process exists for projectID.
func (p *CodexPool) Remove(ctx context.Context, projectID string) error {
	p.mu.Lock()
	entry, ok := p.entries[projectID]
	if !ok {
		p.mu.Unlock()
		return nil
	}
	delete(p.entries, projectID)
	p.mu.Unlock()

	return entry.process.Shutdown(ctx)
}

// Shutdown gracefully terminates all pooled processes.
// Must be called when the aimux daemon is shutting down.
func (p *CodexPool) Shutdown(ctx context.Context) error {
	p.stopOnce.Do(func() { close(p.stopCh) })
	p.wg.Wait()

	p.mu.Lock()
	entries := make(map[string]*poolEntry, len(p.entries))
	for k, v := range p.entries {
		entries[k] = v
	}
	p.entries = make(map[string]*poolEntry)
	p.mu.Unlock()

	var errs []error
	for projectID, entry := range entries {
		if err := entry.process.Shutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("project %q: %w", projectID, err))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("codex: CodexPool.Shutdown: %d errors: %v", len(errs), errs)
	}
	return nil
}

// Len returns the number of active pool entries (thread-safe snapshot).
func (p *CodexPool) Len() int {
	p.mu.Lock()
	n := len(p.entries)
	p.mu.Unlock()
	return n
}

// idleEvictLoop runs in background and shuts down entries idle longer than cfg.IdleTimeout.
func (p *CodexPool) idleEvictLoop() {
	defer p.wg.Done()
	ticker := time.NewTicker(p.cfg.IdleTimeout / 2)
	defer ticker.Stop()

	for {
		select {
		case <-p.stopCh:
			return
		case <-ticker.C:
			p.evictIdle()
		}
	}
}

func (p *CodexPool) evictIdle() {
	now := time.Now()
	p.mu.Lock()
	var toEvict []struct {
		id   string
		proc *AppServerProcess
	}
	for id, entry := range p.entries {
		if now.Sub(entry.lastUsed) > p.cfg.IdleTimeout {
			toEvict = append(toEvict, struct {
				id   string
				proc *AppServerProcess
			}{id, entry.process})
			delete(p.entries, id)
		}
	}
	p.mu.Unlock()

	for _, ev := range toEvict {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_ = ev.proc.Shutdown(ctx)
		cancel()
	}
}
