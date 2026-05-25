package server

import (
	"strings"
	"testing"

	"github.com/thebtf/aimux/pkg/config"
	"github.com/thebtf/aimux/pkg/executor/picker"
)

// TestSplitCommandLine_SingleQuoteInsideDoubleQuoteStaysLiteral verifies the
// security-reviewer's claim that single quotes inside a double-quoted segment
// escape tokenization. The claim is that user prompt containing "'" can inject
// additional CLI arguments because splitCommandLine treats "'" as a quote.
//
// Expected: inside a double-quoted segment, "'" is literal data. The whole
// rendered "-p \"...\" string yields exactly two tokens: -p and the prompt.
func TestSplitCommandLine_SingleQuoteInsideDoubleQuoteStaysLiteral(t *testing.T) {
	// Attacker prompt that tried to break out via single quote.
	rendered := `-p "Hello' --debug --model gemini-2.0-flash-thinking '"`

	got, err := splitCommandLine(rendered)
	if err != nil {
		t.Fatalf("splitCommandLine returned error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 tokens (-p + single-quoted prompt as one element), got %d: %#v", len(got), got)
	}
	if got[0] != "-p" {
		t.Errorf("first token: got %q, want %q", got[0], "-p")
	}
	if !strings.Contains(got[1], "--debug") || !strings.Contains(got[1], "--model") {
		t.Errorf("second token should contain literal injection attempt as data, got %q", got[1])
	}
}

// TestSplitCommandLine_SingleQuoteOutsideQuotesIsActiveQuote documents that
// splitCommandLine DOES recognize "'" as a quote outside any other quote, so a
// template that wraps a field in single quotes (e.g. -p '{{.Prompt}}') WOULD
// be vulnerable to single-quote injection. No current profile does this, but
// commandTemplateArgValue should escape "'" defensively.
func TestSplitCommandLine_SingleQuoteOutsideQuotesIsActiveQuote(t *testing.T) {
	rendered := `-p 'hello world'`
	got, err := splitCommandLine(rendered)
	if err != nil {
		t.Fatalf("splitCommandLine returned error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 tokens, got %d: %#v", len(got), got)
	}
	if got[1] != "hello world" {
		t.Errorf("inside single-quote: got %q, want %q", got[1], "hello world")
	}
}

// TestCommandArgsTemplateArgs_RejectsModelWhitespace verifies the focused fix
// for the actual argument-injection vector: when an unquoted template field
// like "--model {{.Model}}" receives a Model value containing whitespace,
// splitCommandLine tokenizes across whitespace boundaries and the spawned CLI
// receives extra argv elements it did not intend.
//
// Expected behavior: commandArgsTemplateArgs refuses the dispatch (returns
// ok=false) so the caller falls back to the safe argv-based path which passes
// Model as a single argv element via profile.ModelFlag.
func TestCommandArgsTemplateArgs_RejectsModelWhitespace(t *testing.T) {
	profile := &config.CLIProfile{
		Command: config.CommandConfig{
			Base:         "gemini",
			ArgsTemplate: "{{if .Model}}--model {{.Model}}{{end}} -p \"{{.Prompt}}\"",
		},
	}
	spec := picker.TaskSpec{
		Prompt: "ok",
		Model:  "evil --extra-flag injected",
	}

	_, ok := commandArgsTemplateArgs(profile, spec)
	if ok {
		t.Fatal("commandArgsTemplateArgs returned ok=true for Model containing whitespace; expected ok=false so the caller falls back to the safe argv-based path. Whitespace in Model fields renders unquoted into the template and injects extra argv elements via splitCommandLine.")
	}
}

// TestCommandArgsTemplateArgs_AcceptsCleanInput is the positive case proving
// the focused validation does not regress legitimate prompts. Spaces inside
// the Prompt field are still allowed (they live inside a "..."-quoted segment
// in the gemini template), but Model/Effort/SessionID stay free of whitespace.
func TestCommandArgsTemplateArgs_AcceptsCleanInput(t *testing.T) {
	profile := &config.CLIProfile{
		Command: config.CommandConfig{
			Base:         "gemini",
			ArgsTemplate: "{{if .Model}}--model {{.Model}}{{end}} -p \"{{.Prompt}}\"",
		},
	}
	spec := picker.TaskSpec{
		Prompt: "Tell me about the weather today",
		Model:  "gemini-2.0-flash",
	}

	args, ok := commandArgsTemplateArgs(profile, spec)
	if !ok {
		t.Fatal("commandArgsTemplateArgs returned ok=false on legitimate inputs; the focused validation must not regress normal prompts/models")
	}
	// Expect: --model gemini-2.0-flash -p "Tell me about the weather today"
	// → 4 tokens after splitCommandLine
	if len(args) != 4 {
		t.Fatalf("expected 4 argv elements, got %d: %#v", len(args), args)
	}
	if args[0] != "--model" || args[1] != "gemini-2.0-flash" {
		t.Errorf("model args: got [%q %q], want [--model gemini-2.0-flash]", args[0], args[1])
	}
	if args[2] != "-p" || args[3] != "Tell me about the weather today" {
		t.Errorf("prompt args: got [%q %q], want [-p \"Tell me about the weather today\"]", args[2], args[3])
	}
}

// TestCommandArgsTemplateArgs_NewlineInValueIsRejected verifies that control
// characters which cannot be escaped within the splitCommandLine context cause
// the args_template path to refuse the dispatch (returns ok=false → caller
// falls back to flag-based argv path which is safe).
//
// Pre-fix: newline passes through commandTemplateArgValue unchanged and
// splitCommandLine treats it as token boundary.
// Post-fix: commandArgsTemplateArgs rejects values with control chars.
func TestCommandArgsTemplateArgs_NewlineInValueIsRejected(t *testing.T) {
	profile := &config.CLIProfile{
		Command: config.CommandConfig{
			Base:         "gemini",
			ArgsTemplate: "-p \"{{.Prompt}}\"",
		},
	}
	spec := picker.TaskSpec{
		Prompt: "hello\n--extra-flag",
	}

	_, ok := commandArgsTemplateArgs(profile, spec)
	if ok {
		t.Fatal("commandArgsTemplateArgs returned ok=true for prompt containing \\n; expected ok=false so the caller falls back to the safe argv-based path")
	}
}

// TestCommandTemplateArgValue_EscapesSingleQuote is defense-in-depth for the
// case where a profile template wraps a field in single quotes (none currently
// do, but commandTemplateArgValue should escape "'" so that templates may be
// written either way safely).
//
// Pre-fix: "'" passes through unchanged → template '{{.Prompt}}' is escapable.
// Post-fix: "'" → "\'" (or equivalent safe sequence).
//
// Note: splitCommandLine's current behavior treats backslash-escapes only
// inside double quotes; inside single quotes "\'" is two literal characters.
// The point of this test is to document that commandTemplateArgValue produces
// a value where embedded "'" cannot terminate a surrounding single-quote
// template segment. The simplest safe form is to reject "'" outright in the
// args_template path (same as newline). We assert the reject behavior here.
func TestCommandArgsTemplateArgs_SingleQuoteInValueIsRejected(t *testing.T) {
	profile := &config.CLIProfile{
		Command: config.CommandConfig{
			Base:         "gemini",
			ArgsTemplate: "-p \"{{.Prompt}}\"",
		},
	}
	spec := picker.TaskSpec{
		Prompt: "hello' --extra-flag '",
	}

	_, ok := commandArgsTemplateArgs(profile, spec)
	if ok {
		t.Fatal("commandArgsTemplateArgs returned ok=true for prompt containing single quotes; expected ok=false (defense-in-depth — falls back to safe argv-based path)")
	}
}
