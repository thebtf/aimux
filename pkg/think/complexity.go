package think

import (
	"math"
	"reflect"
)

// Complexity scoring weights and thresholds (ported from v2).
const (
	textLengthWeight      = 0.3
	subItemCountWeight    = 0.3
	structuralDepthWeight = 0.2
	patternBiasWeight     = 0.2

	textLengthDivisor     = 500.0  // Characters before score reaches 100
	subItemMultiplier     = 10     // Score per array item
	structuralDepthFactor = 25     // Score per nesting level
	maxMeasureRecursion   = 5      // Max depth for measuring nesting
	maxItemsPerLevel      = 10     // Max items scanned per nesting level
	defaultThreshold      = 60     // Default solo/consensus threshold
	soloOnlyBias          = -50    // Bias for patterns without dialog config
)

// Text fields scanned for length scoring.
var textFields = []string{
	"problem", "issue", "decision", "topic", "task", "thought",
	"problemDefinition", "observation", "question", "modelName",
	"approachName", "domainName", "timeFrame",
}

// Array fields scanned for sub-item count scoring.
var arrayFields = []string{
	"options", "criteria", "subProblems", "dependencies", "risks",
	"assumptions", "alternatives", "premises", "counterarguments",
	"claims", "biases", "entities", "relationships", "states",
	"events", "transitions", "personas", "contributions", "evidence",
	"stakeholders", "fallacies", "uncertainties", "elements", "rules",
	"constraints",
}

// CalculateComplexity computes a 0-100 complexity score for the given input.
// The score determines whether to use "solo" or "consensus" (dialog) mode.
// Default threshold is 60.
func CalculateComplexity(pattern string, input map[string]any, threshold int) ComplexityScore {
	if threshold <= 0 {
		threshold = defaultThreshold
	}

	textLen := scoreTextLength(input)
	subItems := scoreSubItemCount(input)
	depth := scoreStructuralDepth(input)
	bias := getPatternBias(pattern)

	rawScore := float64(textLen)*textLengthWeight + float64(subItems)*subItemCountWeight + float64(depth)*structuralDepthWeight + float64(bias)*patternBiasWeight
	total := clamp(0, 100, int(math.Round(rawScore)))

	rec := "solo"
	if total >= threshold {
		rec = "consensus"
	}

	return ComplexityScore{
		Total:           total,
		TextLength:      textLen,
		SubItemCount:    subItems,
		StructuralDepth: depth,
		PatternBias:     bias,
		Recommendation:  rec,
		Threshold:       threshold,
	}
}

// scoreTextLength scores 0-100 based on max string length across text fields.
func scoreTextLength(input map[string]any) int {
	maxLen := 0
	for _, field := range textFields {
		if val, ok := input[field]; ok {
			if s, ok := val.(string); ok && len(s) > maxLen {
				maxLen = len(s)
			}
		}
	}
	score := int(math.Round(float64(maxLen) / textLengthDivisor * 100.0))
	return min(100, score)
}

// scoreSubItemCount scores 0-100 based on total array items across array fields.
func scoreSubItemCount(input map[string]any) int {
	totalItems := 0
	for _, field := range arrayFields {
		if val, ok := input[field]; ok {
			totalItems += countArrayItems(val)
		}
	}
	score := totalItems * subItemMultiplier
	return min(100, score)
}

// scoreStructuralDepth scores 0-100 based on max nesting depth.
func scoreStructuralDepth(input map[string]any) int {
	maxDepth := 0
	for _, val := range input {
		d := measureDepth(val, 0, maxMeasureRecursion)
		if d > maxDepth {
			maxDepth = d
		}
	}
	score := maxDepth * structuralDepthFactor
	return min(100, score)
}

// getPatternBias returns the complexity bias for a pattern from its dialog config.
// Solo-only patterns (no dialog config) get -50.
func getPatternBias(pattern string) int {
	cfg := GetDialogConfig(pattern)
	if cfg == nil {
		return soloOnlyBias
	}
	return cfg.ComplexityBias
}

// measureDepth recursively measures the nesting depth of a value.
// maxRecursion caps recursion; maxItems limits items scanned per level.
func measureDepth(val any, currentDepth int, maxRecursion int) int {
	if currentDepth >= maxRecursion {
		return currentDepth
	}

	if val == nil {
		return currentDepth
	}

	rv := reflect.ValueOf(val)
	switch rv.Kind() {
	case reflect.Map:
		maxD := currentDepth
		count := 0
		iter := rv.MapRange()
		for iter.Next() {
			if count >= maxItemsPerLevel {
				break
			}
			d := measureDepth(iter.Value().Interface(), currentDepth+1, maxRecursion)
			if d > maxD {
				maxD = d
			}
			count++
		}
		return maxD

	case reflect.Slice, reflect.Array:
		maxD := currentDepth
		limit := rv.Len()
		if limit > maxItemsPerLevel {
			limit = maxItemsPerLevel
		}
		for i := 0; i < limit; i++ {
			d := measureDepth(rv.Index(i).Interface(), currentDepth+1, maxRecursion)
			if d > maxD {
				maxD = d
			}
		}
		return maxD

	default:
		return currentDepth
	}
}

// countArrayItems returns the length of a slice/array value, or 0 if not a slice.
func countArrayItems(val any) int {
	rv := reflect.ValueOf(val)
	if rv.Kind() == reflect.Slice || rv.Kind() == reflect.Array {
		return rv.Len()
	}
	return 0
}

// clamp restricts v to [lo, hi].
func clamp(lo, hi, v int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
