package classifier

import (
	"regexp"
	"sort"
	"strings"
	"unicode"
)

const (
	keywordWeight    = 0.70
	structuralWeight = 0.30
)

var (
	filePathPattern = regexp.MustCompile(`(?i)(?:[\w./\\-]+\.(?:go|py|ts|tsx|js|jsx|rs|java|cs|cpp|c|h|md|yaml|yml|json|toml|sql|sh|ps1))`)
	urlPattern      = regexp.MustCompile(`(?i)\bhttps?://\S+`)
)

type promptFeatures struct {
	lower           string
	tokens          map[string]struct{}
	wordCount       int
	filePathCount   int
	hasURL          bool
	hasCodeFence    bool
	hasDiff         bool
	hasQuestion     bool
	hasSpecPath     bool
	hasQuotedPrompt bool
	isImperative    bool
}

func score(prompt string) []Candidate {
	features := extractFeatures(prompt)
	candidates := make([]Candidate, 0, len(taskClasses))

	for _, taskClass := range taskClasses {
		keywordScore := scoreKeywords(taskClass, features)
		structuralScore := scoreStructural(taskClass, features)
		aggregate := weightedAverage(keywordScore, structuralScore)
		candidates = append(candidates, Candidate{
			TaskClass: taskClass,
			Score:     aggregate,
		})
	}

	sortCandidates(candidates)
	return candidates
}

func extractFeatures(prompt string) promptFeatures {
	lower := strings.ToLower(prompt)
	tokens, words := tokenize(lower)

	return promptFeatures{
		lower:           lower,
		tokens:          tokens,
		wordCount:       words,
		filePathCount:   len(filePathPattern.FindAllString(prompt, -1)),
		hasURL:          urlPattern.MatchString(prompt),
		hasCodeFence:    strings.Contains(prompt, "```"),
		hasDiff:         hasDiffCue(lower),
		hasQuestion:     hasQuestionCue(lower),
		hasSpecPath:     strings.Contains(lower, ".agent/specs") || strings.Contains(lower, ".agent\\specs"),
		hasQuotedPrompt: strings.Contains(lower, "<prompt") || strings.Contains(lower, "```prompt") || strings.Contains(lower, "system message"),
		isImperative:    hasImperativeCue(lower),
	}
}

func tokenize(s string) (map[string]struct{}, int) {
	tokens := map[string]struct{}{}
	var b strings.Builder
	count := 0

	flush := func() {
		if b.Len() == 0 {
			return
		}
		tokens[b.String()] = struct{}{}
		count++
		b.Reset()
	}

	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' {
			b.WriteRune(unicode.ToLower(r))
			continue
		}
		flush()
	}
	flush()

	return tokens, count
}

func scoreKeywords(taskClass string, features promptFeatures) float64 {
	var total float64
	for _, kw := range keywordCorpus[taskClass] {
		if matchesKeyword(kw.Term, features) {
			total += kw.Weight
		}
	}
	return clamp01(total)
}

func matchesKeyword(term string, features promptFeatures) bool {
	term = strings.ToLower(term)
	if strings.ContainsAny(term, " ./\\#") {
		return strings.Contains(features.lower, term)
	}
	_, ok := features.tokens[term]
	return ok
}

func scoreStructural(taskClass string, features promptFeatures) float64 {
	switch taskClass {
	case TaskClassCode:
		return clamp01(
			scoreIf(features.filePathCount > 0, 0.45) +
				scoreIf(features.hasCodeFence, 0.25) +
				scoreIf(features.isImperative, 0.55) +
				scoreIf(strings.Contains(features.lower, "go test") || strings.Contains(features.lower, "build failed"), 0.35),
		)
	case TaskClassReview:
		return clamp01(
			scoreIf(features.hasDiff, 0.55) +
				scoreIf(features.filePathCount > 0, 0.20) +
				scoreIf(strings.Contains(features.lower, "pr #") || strings.Contains(features.lower, "pull request"), 0.35) +
				scoreIf(strings.Contains(features.lower, "head"), 0.20) +
				scoreIf(features.wordCount >= 600, 0.20),
		)
	case TaskClassResearch:
		return clamp01(
			scoreIf(features.hasURL, 0.35) +
				scoreIf(features.hasQuestion, 0.45) +
				scoreIf(strings.Contains(features.lower, "compare"), 0.25) +
				scoreIf(features.wordCount >= 600, 0.20),
		)
	case TaskClassSpec:
		return clamp01(
			scoreIf(features.hasSpecPath, 0.40) +
				scoreIf(strings.Contains(features.lower, "fr-") || strings.Contains(features.lower, "nfr-"), 0.30) +
				scoreIf(strings.Contains(features.lower, "cr-"), 0.20) +
				scoreIf(strings.Contains(features.lower, "user stor"), 0.30),
		)
	case TaskClassPrompt:
		return clamp01(
			scoreIf(features.hasQuotedPrompt, 0.35) +
				scoreIf(strings.Contains(features.lower, "system prompt"), 0.30) +
				scoreIf(strings.Contains(features.lower, "agent"), 0.15) +
				scoreIf(strings.Contains(features.lower, "instructions"), 0.20),
		)
	default:
		return 0
	}
}

func weightedAverage(keywordScore, structuralScore float64) float64 {
	return clamp01(keywordScore*keywordWeight + structuralScore*structuralWeight)
}

func sortCandidates(candidates []Candidate) {
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Score == candidates[j].Score {
			return candidates[i].TaskClass < candidates[j].TaskClass
		}
		return candidates[i].Score > candidates[j].Score
	})
}

func hasDiffCue(lower string) bool {
	return strings.Contains(lower, "diff --git") ||
		strings.Contains(lower, "\n+++") ||
		strings.Contains(lower, "\n---") ||
		strings.Contains(lower, "@@")
}

func hasQuestionCue(lower string) bool {
	trimmed := strings.TrimSpace(lower)
	if strings.Contains(trimmed, "?") {
		return true
	}
	for _, prefix := range []string{"what ", "why ", "how ", "when ", "where ", "compare ", "explain "} {
		if strings.HasPrefix(trimmed, prefix) {
			return true
		}
	}
	return false
}

func hasImperativeCue(lower string) bool {
	trimmed := strings.TrimSpace(lower)
	for _, prefix := range []string{"implement ", "fix ", "add ", "update ", "create ", "refactor ", "modify ", "write tests"} {
		if strings.HasPrefix(trimmed, prefix) {
			return true
		}
	}
	return false
}

func scoreIf(condition bool, score float64) float64 {
	if condition {
		return score
	}
	return 0
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
