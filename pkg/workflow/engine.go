package workflow

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/thebtf/aimux/pkg/dialogue"
	"github.com/thebtf/aimux/pkg/types"
)

// defaultStepTimeout is applied when WorkflowStep.Timeout is zero.
const defaultStepTimeout = 5 * time.Minute

// ExecutorSender is the minimal interface over swarm.Swarm that the Engine uses.
// Defining it here (not in the swarm package) keeps the workflow package
// independently testable with simple mocks.
type ExecutorSender interface {
	// Get returns an opaque handle for the named executor.
	Get(ctx context.Context, name string) (ExecutorHandle, error)

	// Send delivers msg through the handle and returns the response.
	Send(ctx context.Context, h ExecutorHandle, msg types.Message) (*types.Response, error)
}

// ExecutorHandle is an opaque reference to a managed executor.
// The concrete type is determined by the ExecutorSender implementation.
type ExecutorHandle interface {
	// ExecutorName returns the logical name of the executor (e.g., "codex").
	ExecutorName() string
}

// DialogueRunner is the minimal interface over dialogue.Controller used by the Engine.
type DialogueRunner interface {
	// NewDialogue creates a new dialogue session from the given config.
	NewDialogue(config dialogue.DialogueConfig) (*dialogue.Dialogue, error)

	// NextTurn executes the next turn(s) within the dialogue.
	NextTurn(ctx context.Context, d *dialogue.Dialogue) ([]dialogue.DialogueTurn, error)

	// Synthesize produces a combined verdict from all turns in d.
	Synthesize(ctx context.Context, d *dialogue.Dialogue) (*dialogue.Synthesis, error)

	// Close marks the dialogue as complete.
	Close(d *dialogue.Dialogue) error
}

// ParticipantFactory creates a dialogue.Participant backed by an executor.
// Injected so tests can supply mocks without a real Swarm.
type ParticipantFactory func(name string) (dialogue.Participant, error)

// Engine executes workflows by dispatching steps to appropriate subsystems.
// All external dependencies are injected via interfaces — the engine has no
// runtime dependency on real CLI processes.
type Engine struct {
	sender      ExecutorSender
	dialogue    DialogueRunner
	patternFn   func(name string, input map[string]any) (map[string]any, error)
	partFactory ParticipantFactory
}

// New creates a ready-to-use Engine.
//
//   - sender: ExecutorSender implementation (wrap swarm.Swarm in a thin adapter).
//   - dlg: DialogueRunner implementation (pass *dialogue.Controller directly).
//   - patternFn: think pattern dispatcher — receives (patternName, inputMap) and
//     returns the result map or an error.
//   - partFactory: creates a dialogue.Participant for each CLI name; used by
//     ActionDialogue steps. May be nil if no dialogue steps are used.
func New(
	sender ExecutorSender,
	dlg DialogueRunner,
	patternFn func(name string, input map[string]any) (map[string]any, error),
	partFactory ParticipantFactory,
) *Engine {
	return &Engine{
		sender:      sender,
		dialogue:    dlg,
		patternFn:   patternFn,
		partFactory: partFactory,
	}
}

// Execute runs the provided workflow steps sequentially to completion.
// Steps are executed in order; DependsOn is reserved for future DAG support.
// If a Gate step fails, execution stops immediately and a "gated" result is returned.
// Any other step error stops execution and returns a "failed" result.
func (e *Engine) Execute(ctx context.Context, steps []WorkflowStep, input WorkflowInput) (*WorkflowResult, error) {
	if len(steps) == 0 {
		return &WorkflowResult{
			Status:  "completed",
			Summary: "",
		}, nil
	}

	results := make([]StepResult, 0, len(steps))

	// priorSummary accumulates context for format-string injection in subsequent steps.
	priorSummary := buildInitialSummary(input)

	for _, step := range steps {
		timeout := step.Timeout
		if timeout == 0 {
			timeout = defaultStepTimeout
		}

		stepCtx, cancel := context.WithTimeout(ctx, timeout)
		start := time.Now()

		content, err := e.executeStep(stepCtx, step, input, priorSummary)
		cancel()

		dur := time.Since(start)

		if err != nil {
			sr := StepResult{
				Name:     step.Name,
				Action:   step.Action,
				Status:   "failed",
				Content:  err.Error(),
				Duration: dur,
			}
			results = append(results, sr)
			return &WorkflowResult{
				Status:  "failed",
				Steps:   results,
				Summary: fmt.Sprintf("Step %q failed: %s", step.Name, err.Error()),
			}, nil
		}

		status := "completed"
		if step.Action == ActionGate {
			if !e.evaluateGate(step.Config, results) {
				sr := StepResult{
					Name:     step.Name,
					Action:   step.Action,
					Status:   "gated",
					Content:  "gate condition not satisfied",
					Duration: dur,
				}
				results = append(results, sr)
				return &WorkflowResult{
					Status:  "gated",
					Steps:   results,
					Summary: fmt.Sprintf("Workflow gated at step %q", step.Name),
				}, nil
			}
		}

		sr := StepResult{
			Name:     step.Name,
			Action:   step.Action,
			Status:   status,
			Content:  content,
			Duration: dur,
		}
		results = append(results, sr)

		// Extend the running summary for the next step.
		if content != "" {
			priorSummary = content
		}
	}

	// Build final summary from last completed step.
	finalSummary := priorSummary
	if len(results) > 0 {
		last := results[len(results)-1]
		if last.Content != "" {
			finalSummary = last.Content
		}
	}

	return &WorkflowResult{
		Status:  "completed",
		Steps:   results,
		Summary: finalSummary,
	}, nil
}

// executeStep dispatches a single step to the appropriate subsystem.
// It returns the step's textual output or an error.
// Gate steps return an empty string (evaluation is handled by Execute).
func (e *Engine) executeStep(ctx context.Context, step WorkflowStep, input WorkflowInput, priorSummary string) (string, error) {
	switch step.Action {
	case ActionSingleExec:
		return e.runSingleExec(ctx, step, priorSummary)
	case ActionDialogue:
		return e.runDialogue(ctx, step, priorSummary)
	case ActionThinkPattern:
		return e.runThinkPattern(ctx, step, input, priorSummary)
	case ActionGate:
		// Gate evaluation happens in Execute after this call returns.
		return "", nil
	case ActionParallel:
		return e.runParallel(ctx, step, priorSummary)
	default:
		return "", fmt.Errorf("workflow: unknown action %d for step %q", step.Action, step.Name)
	}
}

// runSingleExec sends one prompt to one executor and returns the response content.
func (e *Engine) runSingleExec(ctx context.Context, step WorkflowStep, priorSummary string) (string, error) {
	name, err := resolveExecutorName(step.Config)
	if err != nil {
		return "", fmt.Errorf("step %q: %w", step.Name, err)
	}

	prompt := buildPrompt(step.Config, priorSummary)

	h, err := e.sender.Get(ctx, name)
	if err != nil {
		return "", fmt.Errorf("step %q: get executor %q: %w", step.Name, name, err)
	}

	resp, err := e.sender.Send(ctx, h, types.Message{Content: prompt})
	if err != nil {
		return "", fmt.Errorf("step %q: send to %q: %w", step.Name, name, err)
	}

	return resp.Content, nil
}

// runDialogue creates a dialogue session, runs all turns, and synthesizes the result.
func (e *Engine) runDialogue(ctx context.Context, step WorkflowStep, priorSummary string) (string, error) {
	if e.dialogue == nil {
		return "", fmt.Errorf("step %q: dialogue runner not configured", step.Name)
	}
	if e.partFactory == nil {
		return "", fmt.Errorf("step %q: participant factory not configured", step.Name)
	}

	participantNames, err := configStringSlice(step.Config, "participants")
	if err != nil {
		return "", fmt.Errorf("step %q: %w", step.Name, err)
	}

	participants := make([]dialogue.Participant, 0, len(participantNames))
	for _, pname := range participantNames {
		p, err := e.partFactory(pname)
		if err != nil {
			return "", fmt.Errorf("step %q: create participant %q: %w", step.Name, pname, err)
		}
		participants = append(participants, p)
	}

	mode := parseDialogueMode(configString(step.Config, "mode"))
	maxTurns := configInt(step.Config, "max_turns")
	prompt := buildPrompt(step.Config, priorSummary)

	cfg := dialogue.DialogueConfig{
		Participants: participants,
		Mode:         mode,
		MaxTurns:     maxTurns,
		Topic:        prompt,
		Synthesize:   true,
	}

	d, err := e.dialogue.NewDialogue(cfg)
	if err != nil {
		return "", fmt.Errorf("step %q: new dialogue: %w", step.Name, err)
	}

	// Drive turns until the dialogue is complete.
	for {
		turns, err := e.dialogue.NextTurn(ctx, d)
		if err != nil {
			return "", fmt.Errorf("step %q: next turn: %w", step.Name, err)
		}
		if turns == nil {
			break
		}
	}

	syn, err := e.dialogue.Synthesize(ctx, d)
	if err != nil {
		return "", fmt.Errorf("step %q: synthesize: %w", step.Name, err)
	}

	_ = e.dialogue.Close(d)

	return syn.Content, nil
}

// runThinkPattern invokes a registered think pattern and returns the result content.
func (e *Engine) runThinkPattern(_ context.Context, step WorkflowStep, input WorkflowInput, priorSummary string) (string, error) {
	if e.patternFn == nil {
		return "", fmt.Errorf("step %q: pattern function not configured", step.Name)
	}

	patternName := configString(step.Config, "pattern")
	if patternName == "" {
		return "", fmt.Errorf("step %q: config[\"pattern\"] is required for ActionThinkPattern", step.Name)
	}

	inputKey := configString(step.Config, "input_key")
	if inputKey == "" {
		inputKey = "thought"
	}

	patternInput := map[string]any{
		inputKey: priorSummary,
		"topic":  input.Topic,
	}
	if input.Focus != "" {
		patternInput["focus"] = input.Focus
	}
	for k, v := range input.Extra {
		if _, exists := patternInput[k]; !exists {
			patternInput[k] = v
		}
	}

	result, err := e.patternFn(patternName, patternInput)
	if err != nil {
		return "", fmt.Errorf("step %q: pattern %q: %w", step.Name, patternName, err)
	}

	return extractResultText(result), nil
}

// runParallel dispatches a prompt to multiple executors concurrently and
// concatenates their responses.
func (e *Engine) runParallel(ctx context.Context, step WorkflowStep, priorSummary string) (string, error) {
	clis, err := configStringSlice(step.Config, "clis")
	if err != nil || len(clis) == 0 {
		// Fall back to single CLI via "cli" or "role".
		name, nerr := resolveExecutorName(step.Config)
		if nerr != nil {
			return "", fmt.Errorf("step %q: parallel requires config[\"clis\"] or config[\"cli\"]/config[\"role\"]: %w", step.Name, nerr)
		}
		clis = []string{name}
	}

	prompt := buildPrompt(step.Config, priorSummary)

	type parallelResult struct {
		name    string
		content string
		err     error
	}

	results := make([]parallelResult, len(clis))
	var wg sync.WaitGroup

	for i, name := range clis {
		wg.Add(1)
		go func(idx int, execName string) {
			defer wg.Done()
			h, err := e.sender.Get(ctx, execName)
			if err != nil {
				results[idx] = parallelResult{name: execName, err: fmt.Errorf("get %q: %w", execName, err)}
				return
			}
			resp, err := e.sender.Send(ctx, h, types.Message{Content: prompt})
			if err != nil {
				results[idx] = parallelResult{name: execName, err: fmt.Errorf("send to %q: %w", execName, err)}
				return
			}
			results[idx] = parallelResult{name: execName, content: resp.Content}
		}(i, name)
	}

	wg.Wait()

	var parts []string
	for _, r := range results {
		if r.err != nil {
			return "", fmt.Errorf("step %q: parallel executor %q: %w", step.Name, r.name, r.err)
		}
		parts = append(parts, fmt.Sprintf("[%s]:\n%s", r.name, r.content))
	}

	return strings.Join(parts, "\n\n"), nil
}

// evaluateGate checks whether the gate condition from step.Config is satisfied.
// Currently the only built-in condition is "no_critical_issues" which passes
// if no prior step's Content contains the word "CRITICAL".
func (e *Engine) evaluateGate(config map[string]any, priorResults []StepResult) bool {
	require := configString(config, "require")

	switch require {
	case "no_critical_issues":
		for _, sr := range priorResults {
			if strings.Contains(sr.Content, "CRITICAL") {
				return false
			}
		}
		return true
	default:
		// Unknown condition — pass by default (conservative; prevents hard failures
		// on unknown gates while we grow the condition vocabulary).
		return true
	}
}

// --- helpers ---

// buildInitialSummary produces the seed context string from WorkflowInput.
func buildInitialSummary(input WorkflowInput) string {
	var parts []string
	if input.Topic != "" {
		parts = append(parts, input.Topic)
	}
	if len(input.Files) > 0 {
		parts = append(parts, fmt.Sprintf("Files: %s", strings.Join(input.Files, ", ")))
	}
	if input.Focus != "" {
		parts = append(parts, fmt.Sprintf("Focus: %s", input.Focus))
	}
	return strings.Join(parts, "\n")
}

// buildPrompt formats step.Config["prompt"] with priorSummary, or returns priorSummary
// directly when no prompt template is configured.
func buildPrompt(config map[string]any, priorSummary string) string {
	tmpl := configString(config, "prompt")
	if tmpl == "" {
		return priorSummary
	}
	if strings.Contains(tmpl, "%s") {
		return fmt.Sprintf(tmpl, priorSummary)
	}
	return tmpl
}

// resolveExecutorName extracts the executor name from step config.
// "cli" takes precedence over "role" (both are accepted aliases).
func resolveExecutorName(config map[string]any) (string, error) {
	if name := configString(config, "cli"); name != "" {
		return name, nil
	}
	if name := configString(config, "role"); name != "" {
		return name, nil
	}
	return "", fmt.Errorf("config must contain \"cli\" or \"role\"")
}

// parseDialogueMode converts a mode string to a dialogue.DialogueMode constant.
func parseDialogueMode(mode string) dialogue.DialogueMode {
	switch strings.ToLower(mode) {
	case "parallel":
		return dialogue.ModeParallel
	case "round_robin":
		return dialogue.ModeRoundRobin
	case "stance":
		return dialogue.ModeStance
	default:
		return dialogue.ModeSequential
	}
}

// extractResultText extracts a human-readable string from a think pattern result map.
// It mirrors the priority list used by PatternParticipant.
func extractResultText(result map[string]any) string {
	keys := []string{"content", "output", "response", "result", "thought", "analysis", "summary", "text"}
	for _, k := range keys {
		if v, ok := result[k]; ok {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
		}
	}
	// Fallback: first non-empty string value.
	for _, v := range result {
		if s, ok := v.(string); ok && s != "" {
			return s
		}
	}
	return ""
}

// configString returns config[key] as a string, or "" if absent or not a string.
func configString(config map[string]any, key string) string {
	if config == nil {
		return ""
	}
	v, ok := config[key]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

// configInt returns config[key] as an int, or 0 if absent or not convertible.
func configInt(config map[string]any, key string) int {
	if config == nil {
		return 0
	}
	v, ok := config[key]
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	}
	return 0
}

// configStringSlice returns config[key] as []string.
// Accepts []string and []any (JSON-decoded arrays).
func configStringSlice(config map[string]any, key string) ([]string, error) {
	if config == nil {
		return nil, fmt.Errorf("config is nil, key %q missing", key)
	}
	v, ok := config[key]
	if !ok {
		return nil, fmt.Errorf("config key %q not found", key)
	}
	switch s := v.(type) {
	case []string:
		return s, nil
	case []any:
		result := make([]string, 0, len(s))
		for i, item := range s {
			str, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("config[%q][%d] is not a string", key, i)
			}
			result = append(result, str)
		}
		return result, nil
	default:
		return nil, fmt.Errorf("config[%q] is not a string slice", key)
	}
}
