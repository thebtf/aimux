package think

// PatternMeta holds pattern classification metadata.
type PatternMeta struct {
	IsStateful      bool
	HasDialogConfig bool
}

// patternMeta is the canonical source for pattern classification.
// Any new pattern MUST be registered here.
var patternMeta = map[string]PatternMeta{}

// RegisterPatternMeta registers metadata for a pattern.
func RegisterPatternMeta(name string, meta PatternMeta) {
	patternMeta[name] = meta
}

// GetPatternMeta returns metadata for a pattern.
func GetPatternMeta(name string) (PatternMeta, bool) {
	m, ok := patternMeta[name]
	return m, ok
}

// IsStatefulPattern returns true if the pattern maintains session state.
func IsStatefulPattern(name string) bool {
	m, ok := patternMeta[name]
	return ok && m.IsStateful
}
