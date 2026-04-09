package patterns

import (
	"regexp"
	"sort"
	"strings"
)

// TextAnalysis holds structured information extracted from a free-form text prompt.
type TextAnalysis struct {
	Entities      []string        // proper nouns, tech terms, quoted strings
	Relationships []EntityRelation // inferred subject-verb-object triples
	Gaps          []Gap            // expected domain entities not mentioned
	Negations     []string         // things explicitly excluded
	Questions     []string         // interrogative sentences or phrases
	Complexity    string           // "low", "medium", "high", "epic"
}

// EntityRelation is a simple subject-verb-object triple inferred from text patterns.
type EntityRelation struct {
	Subject string
	Verb    string
	Object  string
}

// Gap describes a domain concept expected but absent from the input.
type Gap struct {
	Expected string
	Why      string
}

// commonNonEntities is the set of capitalised words that are sentence starters
// or generic pronouns, not proper nouns / tech terms.
var commonNonEntities = map[string]struct{}{
	"The": {}, "This": {}, "That": {}, "When": {}, "How": {}, "What": {},
	"Where": {}, "Who": {}, "Why": {}, "Which": {}, "Whether": {}, "If": {},
	"We": {}, "I": {}, "You": {}, "It": {}, "They": {}, "He": {}, "She": {},
	"Our": {}, "Your": {}, "Their": {}, "My": {}, "His": {}, "Her": {},
	"A": {}, "An": {}, "In": {}, "On": {}, "At": {}, "For": {}, "To": {},
	"By": {}, "As": {}, "And": {}, "Or": {}, "But": {}, "So": {}, "With": {},
	"From": {}, "Of": {}, "Is": {}, "Are": {}, "Was": {}, "Were": {},
	"Be": {}, "Been": {}, "Being": {}, "Do": {}, "Does": {}, "Did": {},
	"Will": {}, "Would": {}, "Should": {}, "Could": {}, "Can": {}, "May": {},
	"Might": {}, "Must": {}, "Shall": {}, "Have": {}, "Has": {}, "Had": {},
	"Design": {}, "Create": {}, "Build": {}, "Add": {}, "Use": {}, "Make": {},
	"Implement": {}, "Set": {}, "Get": {}, "Need": {}, "Want": {},
	"Also": {}, "Then": {}, "Now": {}, "Not": {}, "No": {}, "All": {}, "Each": {},
}

var (
	// capitalizedWordRe matches words starting with an uppercase letter followed by
	// alphanumerics (covers OAuth2, JWT, React, RBAC, PostgreSQL, etc.).
	capitalizedWordRe = regexp.MustCompile(`\b[A-Z][A-Za-z0-9]+\b`)

	// quotedStringRe matches text enclosed in double quotes.
	quotedStringRe = regexp.MustCompile(`"([^"]+)"`)

	// hyphenatedTermRe matches technical terms with hyphens or dots containing
	// at least two segments (e.g. role-based-access, e2e-testing, v1.0).
	hyphenatedTermRe = regexp.MustCompile(`\b[a-z][a-z0-9]*(?:[-\.][a-z0-9]+){1,}\b`)

	// relationWithRe: "X with Y"
	relationWithRe = regexp.MustCompile(`(?i)\b(\w[\w-]*)\s+with\s+(\w[\w-]*)`)
	// relationForRe: "X for Y"
	relationForRe = regexp.MustCompile(`(?i)\b(\w[\w-]*)\s+for\s+(\w[\w-]*)`)
	// relationUsingRe: "X using Y"
	relationUsingRe = regexp.MustCompile(`(?i)\b(\w[\w-]*)\s+using\s+(\w[\w-]*)`)
	// negationRe: "without X", "no X", "not X", "exclude X", "avoid X"
	negationRe = regexp.MustCompile(`(?i)\b(?:without|no|not|exclude|avoid)\s+([A-Za-z][\w\s-]{1,40}?)(?:[.,;!?\n]|$)`)
	// questionSentenceRe: sentences ending with "?"
	questionSentenceRe = regexp.MustCompile(`[^.!?]*\?`)
	// questionPhraseRe: phrases opening with interrogative markers (no "?")
	questionPhraseRe = regexp.MustCompile(`(?i)\b(?:should\s+we|which|how\s+to|whether)\b[^.!?\n]{3,}`)
)

// AnalyzeText extracts entities, relationships, negations, questions, gaps, and
// complexity metrics from a free-form problem description.
func AnalyzeText(text string) *TextAnalysis {
	return &TextAnalysis{
		Entities:      extractEntities(text),
		Relationships: inferRelationships(text),
		Negations:     detectNegations(text),
		Questions:     detectQuestions(text),
		Complexity:    estimateComplexity(text),
	}
}

// extractEntities pulls out:
//  1. Capitalised words that aren't sentence starters (OAuth2, JWT, RBAC…)
//  2. Quoted strings ("user registration")
//  3. Hyphenated / dotted technical terms (role-based-access, e2e-testing)
func extractEntities(text string) []string {
	seen := make(map[string]struct{})
	var result []string

	add := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" {
			return
		}
		if _, dup := seen[s]; dup {
			return
		}
		seen[s] = struct{}{}
		result = append(result, s)
	}

	// 1. Capitalised words not in the exclusion set.
	for _, m := range capitalizedWordRe.FindAllString(text, -1) {
		if _, skip := commonNonEntities[m]; !skip {
			add(m)
		}
	}

	// 2. Quoted strings.
	for _, m := range quotedStringRe.FindAllStringSubmatch(text, -1) {
		add(m[1])
	}

	// 3. Hyphenated / dotted technical terms.
	for _, m := range hyphenatedTermRe.FindAllString(text, -1) {
		add(m)
	}

	sort.Strings(result)
	return result
}

// inferRelationships scans for "X with Y", "X for Y", "X using Y" patterns.
// "X without Y" is captured as negation instead.
func inferRelationships(text string) []EntityRelation {
	var rels []EntityRelation
	seen := make(map[string]struct{})

	add := func(subj, verb, obj string) {
		subj = strings.TrimSpace(subj)
		obj = strings.TrimSpace(obj)
		if subj == "" || obj == "" {
			return
		}
		key := subj + "|" + verb + "|" + obj
		if _, dup := seen[key]; dup {
			return
		}
		seen[key] = struct{}{}
		rels = append(rels, EntityRelation{Subject: subj, Verb: verb, Object: obj})
	}

	for _, m := range relationWithRe.FindAllStringSubmatch(text, -1) {
		add(m[1], "includes", m[2])
	}
	for _, m := range relationForRe.FindAllStringSubmatch(text, -1) {
		add(m[1], "serves", m[2])
	}
	for _, m := range relationUsingRe.FindAllStringSubmatch(text, -1) {
		add(m[1], "uses", m[2])
	}

	return rels
}

// detectNegations finds "without X", "no X", "not X", "exclude X", "avoid X".
func detectNegations(text string) []string {
	seen := make(map[string]struct{})
	var result []string

	for _, m := range negationRe.FindAllStringSubmatch(text, -1) {
		phrase := strings.TrimSpace(m[1])
		if phrase == "" {
			continue
		}
		if _, dup := seen[phrase]; dup {
			continue
		}
		seen[phrase] = struct{}{}
		result = append(result, phrase)
	}

	return result
}

// detectQuestions collects:
//  1. Any sentence fragment containing "?"
//  2. Phrases beginning with "should we", "which", "how to", "whether"
func detectQuestions(text string) []string {
	seen := make(map[string]struct{})
	var result []string

	add := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" {
			return
		}
		if _, dup := seen[s]; dup {
			return
		}
		seen[s] = struct{}{}
		result = append(result, s)
	}

	for _, m := range questionSentenceRe.FindAllString(text, -1) {
		add(m)
	}

	for _, m := range questionPhraseRe.FindAllString(text, -1) {
		// Only add if not already captured as a "?" sentence.
		add(m)
	}

	return result
}

// estimateComplexity uses sentence count and conjunction density.
//
//	low:    ≤2 sentences AND ≤1 conjunction
//	medium: 3-5 sentences OR 2-3 conjunctions
//	high:   6-10 sentences OR 4+ conjunctions
//	epic:   >10 sentences
func estimateComplexity(text string) string {
	// Count sentences by splitting on .!?
	sentenceRe := regexp.MustCompile(`[.!?]+`)
	parts := sentenceRe.Split(strings.TrimSpace(text), -1)
	sentences := 0
	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			sentences++
		}
	}

	// Count conjunctions.
	conjRe := regexp.MustCompile(`(?i)\b(and|or|but|also|additionally)\b`)
	conjunctions := len(conjRe.FindAllString(text, -1))

	switch {
	case sentences > 10:
		return "epic"
	case sentences >= 6 || conjunctions >= 4:
		return "high"
	case sentences >= 3 || conjunctions >= 2:
		return "medium"
	default:
		return "low"
	}
}

// DetectGaps compares domain.ExpectedEntities against detectedEntities and
// returns a sorted list of Gap entries for each expected entity not found.
// Returns nil when domain is nil.
func DetectGaps(detectedEntities []string, domain *DomainTemplate) []Gap {
	if domain == nil {
		return nil
	}

	// Build a lowercase set from detected entities for case-insensitive matching.
	detectedLower := make([]string, len(detectedEntities))
	for i, e := range detectedEntities {
		detectedLower[i] = strings.ToLower(e)
	}

	found := func(expected string) bool {
		exp := strings.ToLower(expected)
		for _, d := range detectedLower {
			if strings.Contains(d, exp) || strings.Contains(exp, d) {
				return true
			}
		}
		return false
	}

	var gaps []Gap
	for _, expected := range domain.ExpectedEntities {
		if !found(expected) {
			gaps = append(gaps, Gap{
				Expected: expected,
				Why:      expected + " is typically needed for " + domain.Name + " systems but was not mentioned",
			})
		}
	}

	sort.Slice(gaps, func(i, j int) bool {
		return gaps[i].Expected < gaps[j].Expected
	})

	return gaps
}
