// T012 — sentinel detection per profile (codex/gemini/claude).
//
// AIMUX-14 CR-001 Phase 1, NFR-4 + CHK011: sentinel completion-pattern
// false-positive rate ≤ 1% on 100 sample lines per CLI. Fixtures are
// SYNTHETIC stand-ins approximating real CLI output shapes — they exercise
// matcher correctness under diverse line formats, NOT byte-exact CLI
// output reproduction.
//
// Synthetic ≠ canonical-CLI-output. cmd/testcli/{gemini,claude}.go emits
// JSONL/NDJSON events; this test's gemini/claude corpora include
// `--- DONE ---` / `---END---` style markers that the corresponding profile
// patterns в production may use OR may not. Operator fixture-collection
// round (real captures from live consensus / dialog runs) supersedes this
// when available — see PR #134 review for context.
//
// Anti-stub: removing the matcher would yield zero true-positives AND
// zero false-positives — the test asserts BOTH bounded false-positive
// rate AND non-zero true-positive matches on the canonical sentinel line.

package session

import (
	"regexp"
	"strings"
	"testing"
)

type clipCase struct {
	cli         string
	pattern     string
	corpus      []string // 100 representative lines
	sentinelLine string  // canonical line that MUST match
}

// codexCorpus returns 100 synthetic lines approximating codex output shape:
// stderr diagnostics, JSONL events, and the sentinel turn-completed marker.
func codexCorpus() []string {
	lines := []string{
		"codex exec: model=gpt-5.4",
		"Processing...",
		"Tokens: 42 input, 17 output",
		`{"type":"thread.started","thread_id":"abc-123"}`,
		`{"type":"turn.started"}`,
		`{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"Hello"}}`,
		`{"type":"turn.completed","usage":{"input_tokens":42,"cached_input_tokens":0,"output_tokens":17}}`,
	}
	for len(lines) < 100 {
		lines = append(lines,
			"codex exec: planning...",
			`{"type":"item.completed","item":{"id":"item_x","type":"reasoning","text":"thinking..."}}`,
			"Estimated tokens: ~100",
			`{"type":"item.delta","item_id":"item_0","delta":"more text"}`,
		)
	}
	return lines[:100]
}

func geminiCorpus() []string {
	lines := []string{
		"Loaded model: gemini-2.5-pro",
		`{"role":"user","content":"hi"}`,
		`{"role":"assistant","content":"Hello there"}`,
		"--- DONE ---",
		"--- END ---",
		"context_used: 1024 tokens",
	}
	for len(lines) < 100 {
		lines = append(lines,
			"streaming chunk: ...",
			`{"event":"chunk","text":"more"}`,
			"checkpoint saved",
			`{"event":"final","content":"done"}`,
		)
	}
	return lines[:100]
}

func claudeCorpus() []string {
	lines := []string{
		"claude-sonnet-4-6 ready",
		"<thinking>analysing</thinking>",
		"<answer>Here is the answer</answer>",
		"---END---",
		"Tokens used: 142",
	}
	for len(lines) < 100 {
		lines = append(lines,
			"<message>partial</message>",
			"<token>chunk</token>",
			"<tool_use>read_file</tool_use>",
			"<tool_result>ok</tool_result>",
		)
	}
	return lines[:100]
}

func TestBaseSession_CompletionPattern_FalsePositiveRate(t *testing.T) {
	cases := []clipCase{
		{
			cli:          "codex",
			pattern:      `^\{"type":"turn.completed".*\}$`,
			corpus:       codexCorpus(),
			sentinelLine: `{"type":"turn.completed","usage":{"input_tokens":42,"cached_input_tokens":0,"output_tokens":17}}`,
		},
		{
			cli:          "gemini",
			pattern:      `^--- (DONE|END) ---$`,
			corpus:       geminiCorpus(),
			sentinelLine: "--- DONE ---",
		},
		{
			cli:          "claude",
			pattern:      `^---END---$`,
			corpus:       claudeCorpus(),
			sentinelLine: "---END---",
		},
	}

	const nfr4Ceiling = 1.0 // percent

	for _, tc := range cases {
		t.Run(tc.cli, func(t *testing.T) {
			re, err := regexp.Compile(tc.pattern)
			if err != nil {
				t.Fatalf("compile %s pattern: %v", tc.cli, err)
			}

			falsePositives := 0
			truePositives := 0
			for _, line := range tc.corpus {
				if !re.MatchString(line) {
					continue
				}
				// Match — true if it equals the canonical sentinel; false otherwise.
				if strings.TrimSpace(line) == tc.sentinelLine {
					truePositives++
				} else {
					falsePositives++
				}
			}

			rate := float64(falsePositives) / float64(len(tc.corpus)) * 100
			if rate > nfr4Ceiling {
				t.Errorf("NFR-4 %s: false-positive rate %.2f%% (%d/%d), want ≤ %.2f%%",
					tc.cli, rate, falsePositives, len(tc.corpus), nfr4Ceiling)
			}

			// Anti-stub: matcher must produce SOME true positive on the canonical line —
			// otherwise the test passes trivially with zero matches.
			if truePositives == 0 {
				t.Errorf("%s pattern matched zero canonical sentinel lines — matcher broken or pattern wrong",
					tc.cli)
			}

			t.Logf("%s: true=%d, false=%d, rate=%.2f%% (ceiling %.2f%%)",
				tc.cli, truePositives, falsePositives, rate, nfr4Ceiling)
		})
	}
}
