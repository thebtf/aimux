package loom

import "context"

// requestIDKey is a private type used as a context key for request ID values.
// Clients MUST use WithRequestID and RequestIDFrom helpers — never raw context values.
type requestIDKey struct{}

// RequestIDKey is the exported context key constant for distributed tracing.
// Used in external APIs that need to interoperate with non-loom context flows.
var RequestIDKey = requestIDKey{} //nolint:gochecknoglobals

// WithRequestID returns a new context carrying the given request ID.
// Used by aimux server handlers before Submit to propagate tracing.
func WithRequestID(ctx context.Context, requestID string) context.Context {
	return context.WithValue(ctx, RequestIDKey, requestID)
}

// RequestIDFrom extracts the request ID from the context.
// Returns empty string if no request ID was attached.
func RequestIDFrom(ctx context.Context) string {
	if v, ok := ctx.Value(RequestIDKey).(string); ok {
		return v
	}
	return ""
}
