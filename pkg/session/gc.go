package session

import (
	"context"
	"time"

	"github.com/thebtf/aimux/pkg/logger"
	"github.com/thebtf/aimux/pkg/types"
)

// GCReaper garbage collects expired sessions.
// Uses context-based lifecycle (Constitution P5), not periodic timers.
type GCReaper struct {
	sessions *Registry
	log      *logger.Logger
	ttl      time.Duration
}

// NewGCReaper creates a reaper with the given session TTL.
func NewGCReaper(sessions *Registry, log *logger.Logger, ttlHours int) *GCReaper {
	return &GCReaper{
		sessions: sessions,
		log:      log,
		ttl:      time.Duration(ttlHours) * time.Hour,
	}
}

// Run starts the GC loop. Exits when context is cancelled.
func (g *GCReaper) Run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			g.collect()
		}
	}
}

// collect performs one GC pass.
func (g *GCReaper) collect() {
	now := time.Now()
	reaped := 0

	// Reap expired sessions
	for _, sess := range g.sessions.List("") {
		age := now.Sub(sess.LastActiveAt)
		if age <= g.ttl {
			continue
		}

		// Only reap completed/failed sessions
		if sess.Status == types.SessionStatusCompleted || sess.Status == types.SessionStatusFailed {
			g.sessions.Delete(sess.ID)
			reaped++
		} else if sess.Status == types.SessionStatusCreated && age > time.Hour {
			// Reap stuck sessions (created but never started, >1h)
			g.sessions.Update(sess.ID, func(s *Session) {
				s.Status = types.SessionStatusExpired
			})
			g.sessions.Delete(sess.ID)
			reaped++
		}
	}

	if reaped > 0 {
		g.log.Info("GC: reaped %d expired sessions", reaped)
	}
}

// CollectOnce runs a single GC pass (for manual trigger via sessions(gc)).
func (g *GCReaper) CollectOnce() int {
	before := g.sessions.Count()
	g.collect()
	after := g.sessions.Count()
	return before - after
}
