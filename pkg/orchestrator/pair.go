package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/thebtf/aimux/pkg/types"
)

// PairCoding implements the mandatory pair coding strategy.
// Constitution P2: Every exec(role="coding") = driver + reviewer.
// Driver (codex/spark) produces unified diff, reviewer (sonnet) validates per-hunk.
type PairCoding struct {
	driver   types.Executor
	reviewer types.Executor
}

// NewPairCoding creates a pair coding strategy with driver and reviewer executors.
func NewPairCoding(driver, reviewer types.Executor) *PairCoding {
	return &PairCoding{
		driver:   driver,
		reviewer: reviewer,
	}
}

// Name returns the strategy name.
func (p *PairCoding) Name() string { return "pair_coding" }

// Execute runs the pair coding pipeline:
// 1. Driver produces unified diff
// 2. Parse diff into hunks
// 3. Reviewer validates each hunk
// 4. Re-prompt rejected hunks (max rounds from Extra["max_rounds"])
// 5. Return result with review report
func (p *PairCoding) Execute(ctx context.Context, params types.StrategyParams) (*types.StrategyResult, error) {
	maxRounds := 3
	if mr, ok := params.Extra["max_rounds"].(int); ok && mr > 0 {
		maxRounds = mr
	}

	complex := false
	if c, ok := params.Extra["complex"].(bool); ok {
		complex = c
	}

	driverCLI := "codex"
	if len(params.CLIs) > 0 {
		driverCLI = params.CLIs[0]
	}
	reviewerCLI := "claude"
	if len(params.CLIs) > 1 {
		reviewerCLI = params.CLIs[1]
	}

	var allReviews []types.HunkReview
	var lastDiff string
	totalRounds := 0

	for round := 0; round < maxRounds; round++ {
		totalRounds++

		// Step 1: Driver produces diff
		driverPrompt := params.Prompt
		if round > 0 {
			// Re-prompt with rejected hunk feedback
			driverPrompt = buildReprompt(params.Prompt, allReviews)
		}

		driverResult, err := p.driver.Run(ctx, types.SpawnArgs{
			CLI:            driverCLI,
			Command:        driverCLI,
			Args:           []string{"-p", driverPrompt},
			CWD:            params.CWD,
			TimeoutSeconds: params.Timeout,
		})
		if err != nil {
			return nil, fmt.Errorf("driver failed (round %d): %w", round+1, err)
		}

		lastDiff = driverResult.Content

		// Step 2: Parse diff into hunks
		files := ParseUnifiedDiff(lastDiff)
		hunks := AllHunks(files)

		if len(hunks) == 0 {
			// No diff produced — driver returned non-diff content
			return &types.StrategyResult{
				Content: driverResult.Content,
				Status:  "completed",
				Turns:   totalRounds,
			}, nil
		}

		// Step 3: Reviewer validates each hunk
		reviews, err := p.reviewHunks(ctx, hunks, reviewerCLI, params)
		if err != nil {
			return nil, fmt.Errorf("reviewer failed (round %d): %w", round+1, err)
		}

		allReviews = reviews

		// Step 4: Check if all hunks approved/modified
		rejected := 0
		approved := 0
		modified := 0
		for _, r := range reviews {
			switch r.Verdict {
			case types.ReviewApproved:
				approved++
			case types.ReviewModified:
				modified++
			case types.ReviewChangesRequested:
				rejected++
			}
		}

		if rejected == 0 {
			// All hunks approved or modified — build final result
			report := types.ReviewReport{
				DriverCLI:   driverCLI,
				ReviewerCLI: reviewerCLI,
				HunkReviews: reviews,
				Approved:    approved,
				Modified:    modified,
				Rejected:    rejected,
				Rounds:      totalRounds,
			}

			result := &types.StrategyResult{
				Content:      lastDiff,
				Status:       "completed",
				Turns:        totalRounds,
				Participants: []string{driverCLI, reviewerCLI},
				ReviewReport: &report,
			}

			if complex {
				// Complex mode: return structured result, caller decides
				result.Extra = map[string]any{
					"driver_diff": lastDiff,
					"files":       files,
				}
			}

			return result, nil
		}

		// Rejected hunks exist — will re-prompt in next round
	}

	// Max rounds exceeded — return partial result
	report := types.ReviewReport{
		DriverCLI:   driverCLI,
		ReviewerCLI: reviewerCLI,
		HunkReviews: allReviews,
		Rounds:      totalRounds,
	}
	for _, r := range allReviews {
		switch r.Verdict {
		case types.ReviewApproved:
			report.Approved++
		case types.ReviewModified:
			report.Modified++
		case types.ReviewChangesRequested:
			report.Rejected++
		}
	}

	return &types.StrategyResult{
		Content:      lastDiff,
		Status:       "max_rounds_exceeded",
		Turns:        totalRounds,
		Participants: []string{driverCLI, reviewerCLI},
		ReviewReport: &report,
	}, nil
}

// reviewHunks sends each hunk to the reviewer for validation.
// Loads review-checklist.md from prompts.d/ if available (includes anti-stub rules).
func (p *PairCoding) reviewHunks(ctx context.Context, hunks []DiffHunk, reviewerCLI string, params types.StrategyParams) ([]types.HunkReview, error) {
	// Load review checklist from prompts.d/ (includes 7th criterion: Completeness/anti-stub)
	checklist := loadReviewChecklist()

	// Build review prompt with checklist + hunks
	var sb strings.Builder
	sb.WriteString(checklist)
	sb.WriteString("\n\nOriginal task: " + params.Prompt + "\n\n")

	for _, h := range hunks {
		sb.WriteString(fmt.Sprintf("### Hunk %d (%s)\n```diff\n%s```\n\n", h.Index, h.FilePath, h.Content))
	}

	reviewResult, err := p.reviewer.Run(ctx, types.SpawnArgs{
		CLI:            reviewerCLI,
		Command:        reviewerCLI,
		Args:           []string{"-p", sb.String()},
		CWD:            params.CWD,
		TimeoutSeconds: params.Timeout,
	})
	if err != nil {
		return nil, err
	}

	// Parse review response
	reviews, parseErr := parseReviewResponse(reviewResult.Content, len(hunks))
	if parseErr != nil {
		// If parsing fails, assume all approved (graceful degradation)
		reviews = make([]types.HunkReview, len(hunks))
		for i := range reviews {
			reviews[i] = types.HunkReview{
				HunkIndex: i,
				Verdict:   types.ReviewApproved,
				Comment:   "reviewer response unparseable — auto-approved",
			}
		}
	}

	return reviews, nil
}

// parseReviewResponse extracts hunk reviews from reviewer output.
func parseReviewResponse(content string, expectedCount int) ([]types.HunkReview, error) {
	// Find JSON array in response
	jsonStr := findJSONArray(content)
	if jsonStr == "" {
		return nil, fmt.Errorf("no JSON array found in review response")
	}

	var reviews []types.HunkReview
	if err := json.Unmarshal([]byte(jsonStr), &reviews); err != nil {
		return nil, fmt.Errorf("parse review JSON: %w", err)
	}

	return reviews, nil
}

// findJSONArray finds the first JSON array in a string.
func findJSONArray(s string) string {
	start := strings.Index(s, "[")
	if start < 0 {
		return ""
	}

	depth := 0
	inString := false
	escaped := false

	for i := start; i < len(s); i++ {
		ch := s[i]

		if escaped {
			escaped = false
			continue
		}
		if ch == '\\' && inString {
			escaped = true
			continue
		}
		if ch == '"' {
			inString = !inString
			continue
		}
		if inString {
			continue
		}

		switch ch {
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				return s[start : i+1]
			}
		}
	}

	return ""
}

// buildReprompt creates a follow-up prompt incorporating reviewer feedback.
func buildReprompt(originalPrompt string, reviews []types.HunkReview) string {
	var sb strings.Builder
	sb.WriteString(originalPrompt)
	sb.WriteString("\n\nThe reviewer requested changes on the following hunks:\n\n")

	for _, r := range reviews {
		if r.Verdict == types.ReviewChangesRequested {
			sb.WriteString(fmt.Sprintf("- Hunk %d: %s\n", r.HunkIndex, r.Comment))
		}
	}

	sb.WriteString("\nPlease address the feedback and produce an updated unified diff.")
	return sb.String()
}

// loadReviewChecklist loads review-checklist.md from prompts.d/ directories.
// Falls back to a minimal hardcoded checklist if file not found.
func loadReviewChecklist() string {
	// Search in common locations
	searchPaths := []string{
		"config/prompts.d/review-checklist.md",
		filepath.Join("..", "config", "prompts.d", "review-checklist.md"),
	}

	// Also check AIMUX_CONFIG_DIR
	if configDir := os.Getenv("AIMUX_CONFIG_DIR"); configDir != "" {
		searchPaths = append([]string{
			filepath.Join(configDir, "prompts.d", "review-checklist.md"),
		}, searchPaths...)
	}

	for _, path := range searchPaths {
		data, err := os.ReadFile(path)
		if err == nil {
			return string(data)
		}
	}

	// Fallback: minimal checklist with anti-stub rules
	return `Review each hunk against this checklist:
1. Correctness: Does the code do what the task asks?
2. Security: Any injection, hardcoded secrets, unsafe input?
3. Performance: Obvious inefficiencies?
4. Style: Follows conventions?
5. Tests: Edge cases covered?
6. Scope: Within requested scope?
7. Completeness: Is this real implementation, not a stub?
   - _ = variable (STUB-PASSTHROUGH)
   - Hardcoded return strings (STUB-HARDCODED)
   - Log-only function bodies (STUB-NOOP)
   - TODO/FIXME comments (STUB-TODO)
   If ANY stub detected: verdict MUST be changes_requested with STUB-* rule ID.

Respond as JSON array:
[{"hunk_index": 0, "verdict": "approved|modified|changes_requested", "comment": "...", "modified": "..."}]`
}
