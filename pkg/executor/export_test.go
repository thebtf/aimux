package executor

// SetFallbackVerboseForTest overrides the cached AIMUX_FALLBACK_VERBOSE flag
// for the duration of a test. Call t.Cleanup to restore the previous value:
//
//	old := SetFallbackVerboseForTest(false)
//	t.Cleanup(func() { SetFallbackVerboseForTest(old) })
func SetFallbackVerboseForTest(v bool) bool {
	old := fallbackVerboseFlag.Load()
	fallbackVerboseFlag.Store(v)
	return old
}
