package parser

import (
	"strings"
)

// Finding represents a parsed FINDING: line from text output (audit mode).
type Finding struct {
	Rule     string
	Severity string
	File     string
	Line     int
	Message  string
	Raw      string
}

// ParseTextFindings extracts FINDING: lines from text output.
// Format: FINDING: [SEVERITY] rule_name — message (file:line)
func ParseTextFindings(output string) []Finding {
	var findings []Finding
	lines := strings.Split(output, "\n")

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "FINDING:") {
			continue
		}

		f := parseFindingLine(line)
		if f != nil {
			findings = append(findings, *f)
		}
	}

	return findings
}

// parseFindingLine parses a single FINDING: line.
func parseFindingLine(line string) *Finding {
	// Remove "FINDING: " prefix
	rest := strings.TrimPrefix(line, "FINDING:")
	rest = strings.TrimSpace(rest)

	f := &Finding{Raw: line}

	// Extract severity [CRITICAL], [HIGH], etc.
	if strings.HasPrefix(rest, "[") {
		end := strings.Index(rest, "]")
		if end > 0 {
			f.Severity = rest[1:end]
			rest = strings.TrimSpace(rest[end+1:])
		}
	}

	// Extract rule name (before " — ")
	if idx := strings.Index(rest, " — "); idx > 0 {
		f.Rule = rest[:idx]
		rest = rest[idx+len(" — "):]
	} else if idx := strings.Index(rest, " - "); idx > 0 {
		f.Rule = rest[:idx]
		rest = rest[idx+3:]
	}

	// Extract file reference at end: (file:line)
	if strings.HasSuffix(rest, ")") {
		if parenStart := strings.LastIndex(rest, "("); parenStart > 0 {
			ref := rest[parenStart+1 : len(rest)-1]
			f.Message = strings.TrimSpace(rest[:parenStart])

			// Parse file:line
			if colonIdx := strings.LastIndex(ref, ":"); colonIdx > 0 {
				f.File = ref[:colonIdx]
				// Try to parse line number
				lineStr := ref[colonIdx+1:]
				var lineNum int
				for _, ch := range lineStr {
					if ch >= '0' && ch <= '9' {
						lineNum = lineNum*10 + int(ch-'0')
					} else {
						break
					}
				}
				if lineNum > 0 {
					f.Line = lineNum
				}
			} else {
				f.File = ref
			}
		} else {
			f.Message = rest
		}
	} else {
		f.Message = rest
	}

	return f
}

// ExtractFinalJSON extracts a JSON block at the end of text output.
// Some CLIs produce text output followed by a JSON summary.
func ExtractFinalJSON(output string) string {
	// Look for the last JSON object in the output
	lines := strings.Split(output, "\n")

	// Walk backwards to find the closing brace
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "}" || strings.HasSuffix(line, "}") {
			// Found potential end of JSON — now find its start
			jsonStr := FindOutermostJSON(strings.Join(lines[max(0, i-50):i+1], "\n"))
			if jsonStr != "" {
				return jsonStr
			}
		}
	}

	return ""
}
