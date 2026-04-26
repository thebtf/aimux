package patterns

import (
	"fmt"
	"sort"
	"strconv"
	"time"

	think "github.com/thebtf/aimux/pkg/think"
)

type temporalThinkingPattern struct{}

// NewTemporalThinkingPattern returns the "temporal_thinking" pattern handler.
func NewTemporalThinkingPattern() think.PatternHandler { return &temporalThinkingPattern{} }

func (p *temporalThinkingPattern) Name() string { return "temporal_thinking" }

func (p *temporalThinkingPattern) Description() string {
	return "Analyze temporal aspects: states, events, transitions, and constraints over time"
}

func (p *temporalThinkingPattern) Validate(input map[string]any) (map[string]any, error) {
	timeFrame, ok := input["timeFrame"]
	if !ok {
		return nil, fmt.Errorf("missing required field: timeFrame")
	}
	tf, ok := timeFrame.(string)
	if !ok || tf == "" {
		return nil, fmt.Errorf("field 'timeFrame' must be a non-empty string")
	}
	out := map[string]any{"timeFrame": tf}
	if v, ok := input["states"].([]any); ok {
		out["states"] = v
	}
	if v, ok := input["events"].([]any); ok {
		out["events"] = v
	}
	if v, ok := input["transitions"].([]any); ok {
		out["transitions"] = v
	}
	if v, ok := input["constraints"].([]any); ok {
		out["constraints"] = v
	}
	if v, ok := input["analysis"].(string); ok {
		out["analysis"] = v
	}
	return out, nil
}

func (p *temporalThinkingPattern) SchemaFields() map[string]think.FieldSchema {
	return map[string]think.FieldSchema{
		"timeFrame": {Type: "string", Required: true, Description: "The time frame or period being analyzed"},
		"states":    {Type: "array", Required: false, Description: "States in the temporal model", Items: map[string]any{"type": "string"}},
		"events": {
			Type:        "array",
			Required:    false,
			Description: "Events with timestamps",
			Items: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{"type": "string"},
					"timestamp": map[string]any{
						"oneOf": []map[string]any{{"type": "number"}, {"type": "string"}},
					},
					"time": map[string]any{
						"oneOf": []map[string]any{{"type": "number"}, {"type": "string"}},
					},
				},
			},
		},
		"transitions": {Type: "array", Required: false, Description: "Transitions between states", Items: map[string]any{"type": "string"}},
		"constraints": {Type: "array", Required: false, Description: "Temporal constraints", Items: map[string]any{"type": "string"}},
		"analysis":    {Type: "string", Required: false, Description: "Narrative analysis of the temporal model"},
	}
}

func (p *temporalThinkingPattern) Category() string { return "solo" }

// defaultTemporalPhases returns generic project phases derived from common migration/project keywords.
var defaultTemporalPhases = []string{"planning", "preparation", "execution", "validation", "cutover"}

// suggestTemporalPhases derives phase suggestions from keywords extracted from the timeFrame string.
// Returns sensible defaults when no specific pattern is detected.
func suggestTemporalPhases(keywords []string) []string {
	kwSet := make(map[string]struct{}, len(keywords))
	for _, kw := range keywords {
		kwSet[kw] = struct{}{}
	}

	// Migration/deployment projects.
	for _, kw := range []string{"migration", "migrate", "upgrade", "deploy", "deployment"} {
		if _, ok := kwSet[kw]; ok {
			return []string{"planning", "preparation", "execution", "validation", "cutover"}
		}
	}
	// Release / sprint cycle.
	for _, kw := range []string{"release", "sprint", "iteration", "cycle"} {
		if _, ok := kwSet[kw]; ok {
			return []string{"planning", "development", "testing", "review", "release"}
		}
	}
	// Research / discovery projects.
	for _, kw := range []string{"research", "discovery", "analysis", "investigate"} {
		if _, ok := kwSet[kw]; ok {
			return []string{"scoping", "data-collection", "analysis", "synthesis", "reporting"}
		}
	}
	return defaultTemporalPhases
}

func (p *temporalThinkingPattern) Handle(validInput map[string]any, sessionID string) (*think.ThinkResult, error) {
	timeFrame := validInput["timeFrame"].(string)

	countSlice := func(key string) int {
		if v, ok := validInput[key].([]any); ok {
			return len(v)
		}
		return 0
	}

	stateCount := countSlice("states")
	eventCount := countSlice("events")
	transitionCount := countSlice("transitions")
	constraintCount := countSlice("constraints")

	data := map[string]any{
		"timeFrame":       timeFrame,
		"stateCount":      stateCount,
		"eventCount":      eventCount,
		"transitionCount": transitionCount,
		"constraintCount": constraintCount,
		"totalComponents": stateCount + eventCount + transitionCount + constraintCount,
	}
	// Include narrative analysis in output when provided by the caller.
	if analysis, ok := validInput["analysis"].(string); ok && analysis != "" {
		data["analysis"] = analysis
	}

	// Auto-analysis: when events are empty, derive suggested phases from timeFrame keywords.
	if eventCount == 0 {
		keywords := ExtractKeywords(timeFrame)
		phases := suggestTemporalPhases(keywords)
		data["suggestedPhases"] = phases
		data["autoAnalysis"] = map[string]any{
			"source":   "keyword-analysis",
			"keywords": keywords,
		}
	}

	if events, ok := validInput["events"].([]any); ok && len(events) > 0 {
		if tl := buildTimeline(events); tl != nil {
			data["sortedEvents"] = tl.sortedEvents
			data["totalTimespan"] = tl.totalTimespan
			data["longestGap"] = tl.longestGap
		}
	}

	// Guidance — always included.
	data["guidance"] = BuildGuidance("temporal_thinking",
		func() string {
			if eventCount > 0 {
				return "full"
			}
			return "basic"
		}(),
		[]string{"events", "states", "transitions", "constraints"},
	)

	return think.MakeThinkResult("temporal_thinking", data, sessionID, nil, "visual_reasoning", []string{"totalComponents"}), nil
}

// timedEvent is an event with a resolved numeric timestamp.
type timedEvent struct {
	timestamp float64
	raw       map[string]any
}

// timelineResult holds the computed timeline metrics.
type timelineResult struct {
	sortedEvents  []map[string]any
	totalTimespan float64
	longestGap    map[string]any
}

// buildTimeline extracts timestamps from events (via "time" or "timestamp" fields),
// sorts them, and computes totalTimespan and longestGap.
// Returns nil if any event lacks a parseable timestamp field.
func buildTimeline(events []any) *timelineResult {
	timed := make([]timedEvent, 0, len(events))
	for _, e := range events {
		ev, ok := e.(map[string]any)
		if !ok {
			return nil
		}
		ts, ok := extractTimestamp(ev)
		if !ok {
			return nil
		}
		timed = append(timed, timedEvent{timestamp: ts, raw: ev})
	}

	sort.Slice(timed, func(i, j int) bool {
		return timed[i].timestamp < timed[j].timestamp
	})

	sorted := make([]map[string]any, len(timed))
	for i, te := range timed {
		sorted[i] = te.raw
	}

	totalTimespan := 0.0
	if len(timed) > 1 {
		totalTimespan = timed[len(timed)-1].timestamp - timed[0].timestamp
	}

	longestGap := map[string]any{"start": 0.0, "end": 0.0, "duration": 0.0}
	for i := 1; i < len(timed); i++ {
		duration := timed[i].timestamp - timed[i-1].timestamp
		if duration > longestGap["duration"].(float64) {
			longestGap = map[string]any{
				"start":    timed[i-1].timestamp,
				"end":      timed[i].timestamp,
				"duration": duration,
			}
		}
	}

	return &timelineResult{
		sortedEvents:  sorted,
		totalTimespan: totalTimespan,
		longestGap:    longestGap,
	}
}

// extractTimestamp reads "time" or "timestamp" from an event map and returns
// a float64 value. Accepts numeric values or strings parseable as float64.
func extractTimestamp(ev map[string]any) (float64, bool) {
	for _, field := range []string{"time", "timestamp"} {
		v, exists := ev[field]
		if !exists {
			continue
		}
		switch val := v.(type) {
		case float64:
			return val, true
		case float32:
			return float64(val), true
		case int:
			return float64(val), true
		case int64:
			return float64(val), true
		case string:
			// First try numeric string (e.g. "1714000000").
			if f, err := strconv.ParseFloat(val, 64); err == nil {
				return f, true
			}
			// Fall back to ISO 8601 / RFC 3339 parsing (matches TS v1 Date.parse behaviour).
			for _, layout := range []string{
				time.RFC3339,
				time.RFC3339Nano,
				"2006-01-02T15:04:05Z",
				"2006-01-02",
			} {
				if t, err := time.Parse(layout, val); err == nil {
					return float64(t.UnixMilli()), true
				}
			}
		}
	}
	return 0, false
}
