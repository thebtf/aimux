package server

import (
	"context"

	"github.com/thebtf/aimux/pkg/session"
	"github.com/thebtf/aimux/pkg/tenant"
	"github.com/thebtf/aimux/pkg/types"
)

func (s *Server) listSessionsForContext(ctx context.Context, status types.SessionStatus) []*session.Session {
	all := s.sessions.List(status)
	if s.isOperatorContext(ctx) {
		return all
	}
	tc, ok := TenantContextFromContext(ctx)
	if !ok || tc.TenantID == "" {
		return nil
	}
	out := make([]*session.Session, 0, len(all))
	for _, sess := range all {
		if sessionVisibleToTenant(sess, tc.TenantID) {
			out = append(out, sess)
		}
	}
	return out
}

func (s *Server) getSessionForContext(ctx context.Context, sessionID string) *session.Session {
	sess := s.sessions.Get(sessionID)
	if sess == nil || s.isOperatorContext(ctx) {
		return sess
	}
	tc, ok := TenantContextFromContext(ctx)
	if !ok || !sessionVisibleToTenant(sess, tc.TenantID) {
		return nil
	}
	return sess
}

func sessionVisibleToTenant(sess *session.Session, tenantID string) bool {
	if sess == nil || tenantID == "" {
		return false
	}
	if tenantID == tenant.LegacyDefault {
		return sess.TenantID == "" || sess.TenantID == tenant.LegacyDefault
	}
	return sess.TenantID == tenantID
}
