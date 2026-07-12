package server

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
)

// sensitiveEnvSuffixes lists the suffixes (upper-cased) that identify environment
// variables containing secrets. Any env var whose upper-cased name ends with one
// of these suffixes will be stripped before persisting task.Env to SQLite.
var sensitiveEnvSuffixes = []string{
	"_API_KEY",
	"_TOKEN",
	"_SECRET",
	"_PASSWORD",
	"_KEY",
}

// sensitiveEnvExceptions is the exact-match set of env var names (upper-cased) that
// should NOT be filtered despite matching a sensitive suffix pattern.
var sensitiveEnvExceptions = map[string]struct{}{
	"SSH_AUTH_SOCK": {},
}

// FilterSensitive returns a copy of env with sensitive keys removed.
// Keys whose upper-cased name ends with any of sensitiveEnvSuffixes are dropped,
// unless they appear in sensitiveEnvExceptions. The original map is not modified.
func FilterSensitive(env map[string]string) map[string]string {
	if len(env) == 0 {
		return env
	}
	out := make(map[string]string, len(env))
	for k, v := range env {
		upper := strings.ToUpper(k)
		if _, excepted := sensitiveEnvExceptions[upper]; excepted {
			out[k] = v
			continue
		}
		sensitive := false
		for _, suffix := range sensitiveEnvSuffixes {
			if strings.HasSuffix(upper, suffix) {
				sensitive = true
				break
			}
		}
		if !sensitive {
			out[k] = v
		}
	}
	return out
}

// EnvMetadata returns value-free environment provenance for durable task metadata.
func EnvMetadata(env map[string]string) (keys []string, fingerprint string) {
	if len(env) == 0 {
		return nil, ""
	}
	filtered := FilterSensitive(env)
	keys = make([]string, 0, len(env))
	for key := range filtered {
		keys = append(keys, key)
	}
	for key := range env {
		if _, nonSensitive := filtered[key]; !nonSensitive {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	sum := sha256.Sum256([]byte(strings.Join(keys, "\x00")))
	return keys, hex.EncodeToString(sum[:])
}
