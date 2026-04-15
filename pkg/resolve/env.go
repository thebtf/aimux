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

	// Apply extra overrides (last-write wins per key, case-insensitive dedup).
	for k, v := range extra {
		upper := strings.ToUpper(k)
		// Remove any existing entry for this key.
		filtered := result[:0:len(result)]
		for _, kv := range result {
			idx := strings.IndexByte(kv, '=')
			if idx >= 0 && strings.ToUpper(kv[:idx]) == upper {
				continue
			}
			filtered = append(filtered, kv)
		}
		result = append(filtered, k+"="+v)
	}

	return result
}
