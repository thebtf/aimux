package budget

import (
	"fmt"
	"sort"
	"strings"
)

var FieldWhitelist = map[string][]string{
	"status":                  {"job_id", "status", "progress", "poll_count", "session_id", "error", "content_length"},
	"sessions/list":           {"sessions", "loom_tasks", "sessions_pagination", "loom_pagination"},
	"sessions/info":           {"session", "jobs"},
	"sessions/health":         {},
	"sessions/cancel":         {},
	"sessions/kill":           {},
	"sessions/gc":             {},
	"sessions/refresh-warmup": {},
	"investigate/list":        {"session_id", "topic", "domain", "status", "finding_count"},
	"investigate/status":      {"session_id", "topic", "domain", "status", "finding_count", "coverage_progress"},
	"investigate/recall":      {"session_id", "topic", "finding_count", "content_length"},
	"investigate/start":       {},
	"investigate/finding":     {},
	"investigate/assess":      {},
	"investigate/report":      {},
	"investigate/auto":        {},
	"agents/list":             {"name", "description", "role", "domain"},
	"agents/info":             {"name", "description", "role", "domain", "tools", "when", "content_length"},
	"agents/find":             {"name", "description", "role", "domain"},
	"exec":                    {"job_id", "status"},
	"agent":                   {"job_id", "status", "session_id"},
	"consensus":               {"job_id", "status", "session_id"},
	"debate":                  {"job_id", "status", "session_id"},
	"dialog":                  {"job_id", "status", "session_id"},
	"audit":                   {"job_id", "status"},
	"deepresearch":            {},
	"workflow":                {"job_id", "status", "session_id"},
	"think":                   {},
	"upgrade":                 {},
}

var ContentBearingFields = map[string][]string{
	"status":             {"content"},
	"sessions/info":      {"content"},
	"investigate/recall": {"content", "full_report"},
	"agents/info":        {"content"},
	"exec":               {"content"},
	"agent":              {"content", "transcript"},
	"consensus":          {"content", "transcript"},
	"debate":             {"content", "transcript"},
	"dialog":             {"content", "transcript"},
	"audit":              {"content", "transcript"},
	"workflow":           {"content", "transcript"},
}

var policyMetadataFields = map[string]struct{}{
	"truncated":      {},
	"hint":           {},
	"content_length": {},
	"total":          {},
	"limit":          {},
	"offset":         {},
	"has_more":       {},
	"limit_clamped":  {},
}

func isPolicyMetadataField(field string) bool {
	if _, ok := policyMetadataFields[field]; ok {
		return true
	}
	return strings.HasSuffix(field, "_pagination")
}

// ApplyFields filters result to whitelist/specified fields.
// Returns (filtered map, omitted keys, error).
// Error if fields contains unknown field name (not in whitelist).
// Policy metadata keys always pass through: truncated, hint, content_length, total, limit,
// offset, has_more, limit_clamped, and keys ending in "_pagination".
// Does NOT mutate result.
func ApplyFields(result map[string]any, fields []string, whitelist []string) (map[string]any, []string, error) {
	whitelistSet := make(map[string]struct{}, len(whitelist))
	for _, field := range whitelist {
		whitelistSet[field] = struct{}{}
	}

	keepExplicit := len(fields) > 0
	keepSet := make(map[string]struct{}, max(len(fields), len(whitelist)))

	if keepExplicit {
		for _, field := range fields {
			if !isPolicyMetadataField(field) {
				if _, ok := whitelistSet[field]; !ok {
					return nil, nil, fmt.Errorf("unknown field %q", field)
				}
			}
			keepSet[field] = struct{}{}
		}
	} else if len(whitelist) > 0 {
		for _, field := range whitelist {
			keepSet[field] = struct{}{}
		}
	} else {
		keepSet = nil
	}

	filtered := make(map[string]any, len(result))
	omitted := make([]string, 0)

	for key, value := range result {
		if isPolicyMetadataField(key) {
			filtered[key] = value
			continue
		}

		if keepSet == nil {
			filtered[key] = value
			continue
		}

		if _, ok := keepSet[key]; ok {
			filtered[key] = value
			continue
		}

		omitted = append(omitted, key)
	}

	sort.Strings(omitted)
	return filtered, omitted, nil
}

// ValidateContentBearingFields returns an error if caller supplied content-bearing field without
// include_content=true.
// Error: `field "X" requires include_content=true; fields cannot bypass the content opt-in`
func ValidateContentBearingFields(fields []string, contentBearing []string, includeContent bool) error {
	if includeContent || len(fields) == 0 {
		return nil
	}

	contentBearingSet := make(map[string]struct{}, len(contentBearing))
	for _, field := range contentBearing {
		contentBearingSet[field] = struct{}{}
	}

	for _, field := range fields {
		if _, ok := contentBearingSet[field]; ok {
			return fmt.Errorf("field %q requires include_content=true; fields cannot bypass the content opt-in", field)
		}
	}

	return nil
}

func max(a int, b int) int {
	if a > b {
		return a
	}
	return b
}
