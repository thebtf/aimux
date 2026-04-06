package orchestrator

import (
	"fmt"
	"strings"
)

const (
	DefaultContextWindow = 128000 // tokens
	SafetyFactor         = 0.8
	CharsPerToken        = 3
	DefaultMaxTurnChars  = 20000
	RecentTurnCount      = 2 // Number of recent turns to keep at full content
)

// ComputeDialogBudget takes context window sizes (in tokens) for each participant
// and returns a budget in characters: min(contextWindows) * SafetyFactor * CharsPerToken.
// If the slice is empty, DefaultContextWindow is used. Any value <= 0 is replaced with DefaultContextWindow.
func ComputeDialogBudget(contextWindows []int) int {
	if len(contextWindows) == 0 {
		return int(float64(DefaultContextWindow) * SafetyFactor * CharsPerToken)
	}

	minWindow := 0
	for i, w := range contextWindows {
		effective := w
		if effective <= 0 {
			effective = DefaultContextWindow
		}
		if i == 0 || effective < minWindow {
			minWindow = effective
		}
	}

	return int(float64(minWindow) * SafetyFactor * CharsPerToken)
}

// CompactTurnContent collapses blank lines, strips trailing whitespace per line,
// and truncates to maxChars if the result exceeds that limit.
func CompactTurnContent(content string, maxChars int) string {
	if maxChars <= 0 {
		maxChars = DefaultMaxTurnChars
	}

	// Split into lines, strip trailing whitespace from each
	lines := strings.Split(content, "\n")
	trimmed := make([]string, len(lines))
	for i, line := range lines {
		trimmed[i] = strings.TrimRight(line, " \t\r")
	}
	result := strings.Join(trimmed, "\n")

	// Collapse consecutive blank lines (3+ newlines -> 2 newlines)
	for strings.Contains(result, "\n\n\n") {
		result = strings.ReplaceAll(result, "\n\n\n", "\n\n")
	}

	if len(result) <= maxChars {
		return result
	}

	// Truncate at last paragraph boundary (double newline) before maxChars
	truncated := result[:maxChars]
	if idx := strings.LastIndex(truncated, "\n\n"); idx > 0 {
		return truncated[:idx] + "\n\n... [truncated]"
	}

	// No paragraph boundary found; truncate at last space
	if idx := strings.LastIndex(truncated, " "); idx > 0 {
		return truncated[:idx] + " ... [truncated]"
	}

	return truncated + "... [truncated]"
}

// ExtractSummary returns the first and last paragraphs of content,
// separated by an ellipsis marker. If there are 2 or fewer paragraphs,
// the content is returned as-is.
func ExtractSummary(content string) string {
	paragraphs := strings.Split(content, "\n\n")
	if len(paragraphs) <= 2 {
		return content
	}
	return paragraphs[0] + "\n\n...\n\n" + paragraphs[len(paragraphs)-1]
}

// BuildDialogContext builds a context string from turns that fits within budget.
// The last RecentTurnCount turns use full CompactTurnContent; older turns use ExtractSummary.
// Turns are returned in chronological order.
func BuildDialogContext(turns []turnEntry, budget int) string {
	if len(turns) == 0 {
		return ""
	}

	// Process turns from newest to oldest, collecting formatted entries
	type formattedTurn struct {
		text string
		idx  int
	}

	var collected []formattedTurn
	accumulated := 0

	for i := len(turns) - 1; i >= 0; i-- {
		t := turns[i]
		var content string
		compacted := CompactTurnContent(t.Content, 0)

		distFromEnd := len(turns) - 1 - i
		if distFromEnd < RecentTurnCount {
			content = compacted
		} else {
			content = ExtractSummary(compacted)
		}

		entry := fmt.Sprintf("[Turn %d - %s] %s\n\n", t.Turn, t.CLI, content)

		if accumulated+len(entry) > budget && len(collected) > 0 {
			break
		}

		accumulated += len(entry)
		collected = append(collected, formattedTurn{text: entry, idx: i})
	}

	// Reverse to chronological order
	var sb strings.Builder
	for i := len(collected) - 1; i >= 0; i-- {
		sb.WriteString(collected[i].text)
	}

	return sb.String()
}

// BuildSynthesisPrompt builds a prompt for synthesizing multiple responses about a topic.
// If the total exceeds budget, each response is truncated proportionally.
func BuildSynthesisPrompt(topic string, responses []string, budget int) string {
	header := fmt.Sprintf("Synthesize the following responses about: %s\n\n", topic)

	if len(responses) == 0 {
		return header
	}

	// Build response sections
	sections := make([]string, len(responses))
	total := len(header)
	for i, r := range responses {
		sections[i] = fmt.Sprintf("## Response %d\n%s\n\n", i+1, r)
		total += len(sections[i])
	}

	if total <= budget {
		var sb strings.Builder
		sb.WriteString(header)
		for _, s := range sections {
			sb.WriteString(s)
		}
		return sb.String()
	}

	// Truncate each response proportionally
	overhead := len(header)
	// Each section has "## Response N\n" prefix and "\n\n" suffix
	for i := range responses {
		prefix := fmt.Sprintf("## Response %d\n", i+1)
		overhead += len(prefix) + len("\n\n")
	}

	available := budget - overhead
	if available <= 0 {
		return header
	}

	perResponse := available / len(responses)

	var sb strings.Builder
	sb.WriteString(header)
	for i, r := range responses {
		truncated := r
		if len(truncated) > perResponse {
			truncated = truncated[:perResponse]
			// Try to cut at a space boundary
			if idx := strings.LastIndex(truncated, " "); idx > 0 {
				truncated = truncated[:idx]
			}
			truncated += "..."
		}
		sb.WriteString(fmt.Sprintf("## Response %d\n%s\n\n", i+1, truncated))
	}

	return sb.String()
}
