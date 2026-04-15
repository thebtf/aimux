package resolve

import (
	"os"
	"strings"

	"github.com/thebtf/aimux/pkg/config"
)

// baselineEnvVars is the OS-essential set always passed to spawned CLIs regardless
// of their profile allowlist. These contain no secrets and are required for basic
// process operation (PATH lookup, home dir, locale, temp dirs).
var baselineEnvVars = []string{
	"PATH", "HOME", "USER", "USERNAME", "LANG", "LC_ALL", "TZ",
	"TERM", "SHELL", "PATHEXT", "SYSTEMROOT", "WINDIR",
	"TEMP", "TMP", "TMPDIR",
	"APPDATA", "LOCALAPPDATA", "PROGRAMFILES", "PROGRAMDATA",
	"USERPROFILE", "HOMEDRIVE", "HOMEPATH", "COMSPEC",
}

// BuildEnv constructs a minimal, allowlisted environment for a spawned CLI process.
// It returns the OS-essential baseline vars + any vars named in profile.EnvPassthrough
// (read from the parent process environment) + the extra overrides map.
//
// Any parent environment variable NOT in the baseline or the profile allowlist is
// silently dropped. This prevents cross-CLI API key leakage in multi-user SSE/HTTP
// deployments.
//
// The extra map is applied last and overrides both baseline and passthrough values.
// extra is intended for per-session injected values (e.g. API keys from ProjectContext).
func BuildEnv(profile *config.CLIProfile, extra map[string]string) []string {
	// Build lookup set: baseline + profile allowlist
	allowed := make(map[string]struct{}, len(baselineEnvVars)+len(profile.EnvPassthrough))
	for _, k := range baselineEnvVars {
		allowed[strings.ToUpper(k)] = struct{}{}
	}
	for _, k := range profile.EnvPassthrough {
		allowed[strings.ToUpper(k)] = struct{}{}
	}

	// Collect matching vars from the parent environment.
	parentEnv := os.Environ()
	result := make([]string, 0, len(parentEnv)+len(extra))

	for _, kv := range parentEnv {
		idx := strings.IndexByte(kv, '=')
		if idx < 0 {
			continue
		}
		key := kv[:idx]
		if _, ok := allowed[strings.ToUpper(key)]; ok {
			result = append(result, kv)
		}
	}

	// Apply extra overrides (case-insensitive dedup, O(N+M)).
	// Normalise the extra map into uppercase key → "key=value" string.
	// If two keys in extra differ only by case, the last one encountered during
	// Go map iteration wins (non-deterministic but benign — callers should not
	// pass case-colliding keys).
	if len(extra) > 0 {
		overrides := make(map[string]string, len(extra))
		for k, v := range extra {
			overrides[strings.ToUpper(k)] = k + "=" + v
		}

		// Single pass: drop any result entries whose key is overridden.
		filtered := make([]string, 0, len(result))
		for _, kv := range result {
			idx := strings.IndexByte(kv, '=')
			if idx >= 0 {
				if _, exists := overrides[strings.ToUpper(kv[:idx])]; exists {
					continue
				}
			}
			filtered = append(filtered, kv)
		}

		// Append override values.
		for _, kv := range overrides {
			filtered = append(filtered, kv)
		}
		return filtered
	}

	return result
}
