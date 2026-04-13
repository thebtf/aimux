package policies

// extractInput is a generic helper that converts an opaque StateSnapshot to the
// concrete policy input type T. It handles both pointer and value forms, and
// returns the zero value of T when the snapshot is nil or has an unexpected type.
func extractInput[T any](snapshot any) T {
	var zero T
	if snapshot == nil {
		return zero
	}
	if v, ok := snapshot.(*T); ok && v != nil {
		return *v
	}
	if v, ok := snapshot.(T); ok {
		return v
	}
	return zero
}
