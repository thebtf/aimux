package hooks

// BeforeHookContext holds context for pre-execution hooks.
type BeforeHookContext struct {
	CLI           string            `json:"cli"`
	PromptPreview string            `json:"prompt_preview"` // truncated to 500 chars
	CWD           string            `json:"cwd"`
	Model         string            `json:"model"`
	Role          string            `json:"role"`
	SessionID     string            `json:"session_id"`
	Metadata      map[string]string `json:"metadata"`
}

// BeforeHookResult is the outcome of a before hook.
type BeforeHookResult struct {
	Action           string            `json:"action"` // "proceed", "block", "skip"
	ModifiedPrompt   string            `json:"modified_prompt,omitempty"`
	Reason           string            `json:"reason,omitempty"`
	SyntheticContent string            `json:"synthetic_content,omitempty"`
	Metadata         map[string]string `json:"metadata,omitempty"`
}

// AfterHookContext extends BeforeHookContext with execution results.
type AfterHookContext struct {
	BeforeHookContext
	Content    string `json:"content"`
	ExitCode   int    `json:"exit_code"`
	DurationMs int64  `json:"duration_ms"`
}

// AfterHookResult is the outcome of an after hook.
type AfterHookResult struct {
	Action      string            `json:"action"` // "accept", "annotate", "reject"
	Reason      string            `json:"reason,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

// BeforeHookFn is a before-execution hook function.
type BeforeHookFn func(ctx BeforeHookContext) BeforeHookResult

// AfterHookFn is an after-execution hook function.
type AfterHookFn func(ctx AfterHookContext) AfterHookResult
