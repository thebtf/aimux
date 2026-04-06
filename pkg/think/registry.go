package think

import (
	"fmt"
	"sort"
	"sync"
)

var (
	patternsMu sync.RWMutex
	patterns   = make(map[string]PatternHandler)
)

// RegisterPattern adds a handler to the global registry. Panics on duplicate.
func RegisterPattern(handler PatternHandler) {
	patternsMu.Lock()
	defer patternsMu.Unlock()

	name := handler.Name()
	if _, exists := patterns[name]; exists {
		panic(fmt.Sprintf("think: duplicate pattern registration: %q", name))
	}
	patterns[name] = handler
}

// GetPattern returns a handler by name, or nil if not found.
func GetPattern(name string) PatternHandler {
	patternsMu.RLock()
	defer patternsMu.RUnlock()
	return patterns[name]
}

// GetAllPatterns returns a sorted list of all registered pattern names.
func GetAllPatterns() []string {
	patternsMu.RLock()
	defer patternsMu.RUnlock()

	names := make([]string, 0, len(patterns))
	for name := range patterns {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// ClearPatterns removes all registered patterns (testing only).
func ClearPatterns() {
	patternsMu.Lock()
	defer patternsMu.Unlock()
	patterns = make(map[string]PatternHandler)
}
