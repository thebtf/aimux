package session

import (
	"context"
	"sync"

	"github.com/thebtf/aimux/pkg/types"
)

// LiveSession wraps a persistent CLI process for multi-turn interaction.
// The process stays alive across multiple Send() calls (LiveStateful mode).
type LiveSession struct {
	session  *Session
	process  types.Session // from executor.Start()
	ctx      context.Context
	cancel   context.CancelFunc
	mu       sync.Mutex
}

// NewLiveSession creates a live session wrapping an executor session.
func NewLiveSession(sess *Session, process types.Session, ctx context.Context) *LiveSession {
	ctx, cancel := context.WithCancel(ctx)
	return &LiveSession{
		session: sess,
		process: process,
		ctx:     ctx,
		cancel:  cancel,
	}
}

// Send sends a prompt to the persistent process and returns the response.
func (ls *LiveSession) Send(ctx context.Context, prompt string) (*types.Result, error) {
	ls.mu.Lock()
	defer ls.mu.Unlock()

	ls.session.Turns++
	return ls.process.Send(ctx, prompt)
}

// Stream sends a prompt and returns a streaming event channel.
func (ls *LiveSession) Stream(ctx context.Context, prompt string) (<-chan types.Event, error) {
	ls.mu.Lock()
	defer ls.mu.Unlock()

	ls.session.Turns++
	return ls.process.Stream(ctx, prompt)
}

// Close terminates the persistent process.
func (ls *LiveSession) Close() error {
	ls.cancel()
	return ls.process.Close()
}

// Alive returns true if the underlying process is running.
func (ls *LiveSession) Alive() bool {
	return ls.process.Alive()
}

// Session returns the tracked session metadata.
func (ls *LiveSession) Session() *Session {
	return ls.session
}
