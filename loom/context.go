package loom

import "context"

// RequestIDKey is the context key type used to attach request IDs to
// loom-managed contexts. Exported so external callers can read request IDs
// off contexts produced by WithRequestID without going through loom helpers.
//
// Use as a value: context.Value(loom.RequestIDKey{})
// Prefer the helpers WithRequestID and RequestIDFrom where possible.
type RequestIDKey struct{}

// WithRequestID returns a new context carrying the given request ID.
// Used by aimux server handlers before Submit to propagate tracing.
func WithRequestID(ctx context.Context, requestID string) context.Context {
	return context.WithValue(ctx, RequestIDKey{}, requestID)
}

// RequestIDFrom extracts the request ID from the context.
// Returns empty string if no request ID was attached.
func RequestIDFrom(ctx context.Context) string {
	if v, ok := ctx.Value(RequestIDKey{}).(string); ok {
		return v
	}
	return ""
}
