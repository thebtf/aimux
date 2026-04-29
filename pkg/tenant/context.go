package tenant

import "context"

// tenantContextKey is the unexported context key used to store a TenantContext
// in a context.Context. Using a private type prevents collisions with keys from
// other packages. This is the canonical definition — pkg/server.tenantContextKey
// was the pre-AIMUX-13 location; it is now a thin alias that delegates here.
type tenantContextKey struct{}

// FromContext retrieves the TenantContext injected by DispatchMiddleware (or any
// caller that used WithContext). Returns (TenantContext, true) when present,
// (zero, false) when absent.
//
// Callers that need a non-empty TenantID when absent should fall back to
// LegacyDefault:
//
//	tc, ok := tenant.FromContext(ctx)
//	if !ok {
//	    tc = tenant.NewLegacyDefaultContext("")
//	}
func FromContext(ctx context.Context) (TenantContext, bool) {
	tc, ok := ctx.Value(tenantContextKey{}).(TenantContext)
	return tc, ok
}

// WithContext returns a new context carrying tc under the tenantContextKey.
// Use in tests to inject a specific tenant identity without going through
// DispatchMiddleware. Production code should use DispatchMiddleware.WithContext
// which calls this function internally.
func WithContext(ctx context.Context, tc TenantContext) context.Context {
	return context.WithValue(ctx, tenantContextKey{}, tc)
}
