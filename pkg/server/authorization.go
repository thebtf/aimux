package server

import (
	"context"
	"fmt"

	"github.com/thebtf/aimux/pkg/tenant"
)

func (s *Server) requireOperator(ctx context.Context, action string) error {
	if s.isOperatorContext(ctx) {
		return nil
	}
	return fmt.Errorf("%s requires tenant role %q", action, tenant.RoleOperator)
}

func (s *Server) isOperatorContext(ctx context.Context) bool {
	tc, ok := TenantContextFromContext(ctx)
	if !ok {
		// Legacy direct calls and single-tenant transports predate TenantContext.
		// In multi-tenant mode, missing context is a hard authorization failure.
		return s == nil || s.dispatchMW == nil || !s.dispatchMW.IsMultiTenant()
	}
	return tc.Role == tenant.RoleOperator
}

func (s *Server) tenantRoleForID(tenantID string) string {
	if tenantID == "" || tenantID == tenant.LegacyDefault {
		if s == nil || s.dispatchMW == nil || !s.dispatchMW.IsMultiTenant() {
			return tenant.RoleOperator
		}
		return ""
	}
	if s != nil && s.dispatchMW != nil {
		if cfg, ok := s.dispatchMW.ResolveTenantByName(tenantID); ok {
			return cfg.Role
		}
		if s.dispatchMW.IsMultiTenant() {
			return ""
		}
	}
	return tenant.RoleOperator
}
