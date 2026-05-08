package code

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/thebtf/aimux/loom"
	applygate "github.com/thebtf/aimux/pkg/executor/code/gate"
	"github.com/thebtf/aimux/pkg/executor/types"
)

const WorkerTypeCode loom.WorkerType = "code"

// PairRoundRunner runs one Strong-Style pair round.
type PairRoundRunner interface {
	RunRound(ctx context.Context, prompt string, criteria SuccessCriteria, cfg PairConfig) (Verdict, error)
}

// PairRoundFunc adapts a function to PairRoundRunner.
type PairRoundFunc func(ctx context.Context, prompt string, criteria SuccessCriteria, cfg PairConfig) (Verdict, error)

// RunRound implements PairRoundRunner.
func (f PairRoundFunc) RunRound(ctx context.Context, prompt string, criteria SuccessCriteria, cfg PairConfig) (Verdict, error) {
	return f(ctx, prompt, criteria, cfg)
}

// GateRunner runs the post-apply gate.
type GateRunner interface {
	Run(ctx context.Context, project applygate.Project) applygate.Result
}

// GateRunnerFunc adapts a function to GateRunner.
type GateRunnerFunc func(ctx context.Context, project applygate.Project) applygate.Result

// Run implements GateRunner.
func (f GateRunnerFunc) Run(ctx context.Context, project applygate.Project) applygate.Result {
	return f(ctx, project)
}

// ApplyFunc writes an approved diff to disk.
type ApplyFunc func(ctx context.Context, diff string, project Project) (filesModified int, hunksApplied int, err error)

// ResumeDelegate is the planned driver-CLI resume adapter shape.
type ResumeDelegate interface {
	ResumeFromTask(ctx context.Context, prevTaskID string) (map[string]any, error)
}

// PairSelector chooses healthy cross-family CLIs for Strong-Style code work.
type PairSelector interface {
	PickPair(ctx context.Context, taskClass string) (driver, navigator types.CLIName, err error)
}

type driverPairSelector interface {
	PickPairForDriver(ctx context.Context, taskClass string, driver types.CLIName) (types.CLIName, types.CLIName, error)
}

// CodeWorkerConfig holds CodeWorker dependencies and defaults.
type CodeWorkerConfig struct {
	Loom                LoomClient
	PairRunner          PairRoundRunner
	GateRunner          GateRunner
	Apply               ApplyFunc
	DriverResumer       ResumeDelegate
	PairSelector        PairSelector
	DriverCLI           types.CLIName
	NavigatorCLI        types.CLIName
	MaxRounds           int
	ConfidenceThreshold float64
}

// CodeWorker orchestrates Strong-Style code execution.
type CodeWorker struct {
	loom                LoomClient
	pairRunner          PairRoundRunner
	gateRunner          GateRunner
	apply               ApplyFunc
	driverResumer       ResumeDelegate
	pairSelector        PairSelector
	driverCLI           types.CLIName
	navigatorCLI        types.CLIName
	maxRounds           int
	confidenceThreshold float64
}

// NewCodeWorker constructs a CodeWorker.
func NewCodeWorker(cfg CodeWorkerConfig) (*CodeWorker, error) {
	if cfg.Loom == nil {
		return nil, types.NewCapabilityMismatch("code worker Loom client is required", nil)
	}
	pairRunner := cfg.PairRunner
	if pairRunner == nil {
		pairRunner = PairRoundFunc(RunRound)
	}
	gateRunner := cfg.GateRunner
	if gateRunner == nil {
		gateRunner = GateRunnerFunc(applygate.Run)
	}
	apply := cfg.Apply
	if apply == nil {
		apply = WriteDiff
	}
	maxRounds := cfg.MaxRounds
	if maxRounds == 0 {
		maxRounds = DefaultMaxRounds
	}
	threshold := cfg.ConfidenceThreshold
	if threshold == 0 {
		threshold = 0.85
	}
	driverCLI := cfg.DriverCLI
	if driverCLI == "" {
		driverCLI = "codex"
	}

	return &CodeWorker{
		loom:                cfg.Loom,
		pairRunner:          pairRunner,
		gateRunner:          gateRunner,
		apply:               apply,
		driverResumer:       cfg.DriverResumer,
		pairSelector:        cfg.PairSelector,
		driverCLI:           driverCLI,
		navigatorCLI:        cfg.NavigatorCLI,
		maxRounds:           maxRounds,
		confidenceThreshold: threshold,
	}, nil
}

// Type implements loom.Worker.
func (w *CodeWorker) Type() loom.WorkerType {
	return WorkerTypeCode
}

// Execute implements loom.Worker.
func (w *CodeWorker) Execute(ctx context.Context, task *loom.Task) (*loom.WorkerResult, error) {
	if task == nil {
		return nil, types.NewUserInputError("code worker task is nil", nil)
	}
	resumeMeta, err := w.hydrateResumeMetadata(ctx, task)
	if err != nil {
		return nil, err
	}
	criteria := DefaultSuccessCriteria(task.Metadata != nil && task.Metadata["spec_active"] == true)
	machine, cliErr := NewMachine(Config{MaxRounds: w.maxRounds, Metadata: task.Metadata})
	if cliErr != nil {
		return w.failTask(task, machine, cliErr)
	}

	driverCLI, navigatorCLI, err := w.pairCLIsForTask(ctx, task)
	if err != nil {
		return w.failTask(task, machine, err)
	}
	recordSelectedPair(task, driverCLI, navigatorCLI)

	prompt := task.Prompt
	var lastVerdict Verdict
	if cliErr := machine.Advance(StateDriver, "code worker prepared pair round"); cliErr != nil {
		return w.failTask(task, machine, cliErr)
	}

	for {
		verdict, err := w.pairRunner.RunRound(ctx, prompt, criteria, PairConfig{
			Loom:           w.loom,
			ParentTaskID:   task.ID,
			ProjectID:      task.ProjectID,
			RequestID:      task.RequestID,
			TenantID:       task.TenantID,
			CWD:            task.CWD,
			ResumeMetadata: resumeMeta,
			DriverCLI:      driverCLI,
			NavigatorCLI:   navigatorCLI,
			Model:          task.Model,
			Effort:         task.Effort,
			Sandbox:        sandboxForTask(task),
			TaskTimeout:    pairTaskTimeout(task),
		})
		if err != nil {
			return w.failTask(task, machine, err)
		}
		lastVerdict = verdict
		if cliErr := machine.Advance(StateNavigator, "pair round returned navigator verdict"); cliErr != nil {
			return w.failTask(task, machine, cliErr)
		}
		w.recordTaskMetadata(task, machine, criteria, lastVerdict, "")

		switch verdict.Action {
		case StateApply, StateRevise:
			result, err := w.applyAndGate(ctx, task, machine, verdict)
			if err != nil {
				return result, err
			}
			return result, nil
		case StateRetry:
			if cliErr := machine.Advance(StateRetry, "navigator requested retry"); cliErr != nil {
				return w.failTask(task, machine, cliErr)
			}
			if cliErr := machine.Advance(StateDriver, "retrying with navigator feedback"); cliErr != nil {
				return w.failTask(task, machine, cliErr)
			}
			prompt = promptWithFeedback(task.Prompt, verdict.Feedback)
			w.recordTaskMetadata(task, machine, criteria, lastVerdict, "")
		case StateEscalate:
			if cliErr := machine.Advance(StateEscalate, "navigator escalated"); cliErr != nil {
				return w.failTask(task, machine, cliErr)
			}
			return w.failTask(task, machine, types.NewCapabilityMismatch("code worker escalated: "+verdictEvidence(verdict), nil))
		default:
			return w.failTask(task, machine, types.NewUserInputError(fmt.Sprintf("unsupported code verdict %s", verdict.Action), nil))
		}
	}
}

func (w *CodeWorker) applyAndGate(ctx context.Context, task *loom.Task, machine *Machine, verdict Verdict) (*loom.WorkerResult, error) {
	if readOnlySandboxForTask(task) {
		return w.failTask(task, machine, types.NewSandboxDenial("read-only sandbox forbids applying code diff", nil))
	}
	if verdict.Confidence < w.confidenceThreshold {
		return w.failTask(task, machine, types.NewCapabilityMismatch(
			fmt.Sprintf("navigator confidence %.2f below threshold %.2f", verdict.Confidence, w.confidenceThreshold),
			nil,
		))
	}
	targetState := verdict.Action
	if cliErr := machine.Advance(targetState, "navigator approved diff for "+strings.ToLower(string(targetState))); cliErr != nil {
		return w.failTask(task, machine, cliErr)
	}
	if _, _, err := w.apply(ctx, verdict.Diff, Project{CWD: task.CWD}); err != nil {
		var cliErr *types.CLIError
		if errors.As(err, &cliErr) {
			return w.failTask(task, machine, cliErr)
		}
		return w.failTask(task, machine, types.NewUserInputError("apply diff failed: "+err.Error(), err))
	}
	if cliErr := machine.Advance(StateGate, "diff written to disk"); cliErr != nil {
		return w.failTask(task, machine, cliErr)
	}

	gateResult := w.gateRunner.Run(ctx, applygate.Project{CWD: task.CWD})
	w.recordTaskMetadata(task, machine, DefaultSuccessCriteria(task.Metadata != nil && task.Metadata["spec_active"] == true), verdict, string(gateResult.Status))
	if gateResult.Status == applygate.StatusFailed {
		if cliErr := machine.Advance(StateError, "apply gate failed"); cliErr != nil {
			return w.failTask(task, machine, cliErr)
		}
		return w.failTask(task, machine, types.NewUserInputError("code gate failed: "+gateResult.Reason, nil))
	}
	if cliErr := machine.Advance(StateDone, "apply gate "+string(gateResult.Status)); cliErr != nil {
		return w.failTask(task, machine, cliErr)
	}
	w.recordTaskMetadata(task, machine, DefaultSuccessCriteria(task.Metadata != nil && task.Metadata["spec_active"] == true), verdict, string(gateResult.Status))
	return &loom.WorkerResult{
		Content:  "code task completed",
		Metadata: cloneMetadata(task.Metadata),
	}, nil
}

func sandboxForTask(task *loom.Task) string {
	if task == nil {
		return ""
	}
	sandbox, _ := metadataString(task.Metadata, "sandbox")
	return strings.TrimSpace(sandbox)
}

func readOnlySandboxForTask(task *loom.Task) bool {
	return sandboxForTask(task) == "read-only"
}

func (w *CodeWorker) failTask(task *loom.Task, machine *Machine, err error) (*loom.WorkerResult, error) {
	cliErr := ensureCLIError(err)
	task.Error = cliErr.Error()
	if machine != nil {
		w.recordTaskMetadata(task, machine, DefaultSuccessCriteria(task.Metadata != nil && task.Metadata["spec_active"] == true), Verdict{}, "")
	}
	return nil, cliErr
}

func (w *CodeWorker) hydrateResumeMetadata(ctx context.Context, task *loom.Task) (map[string]any, error) {
	resumeTaskID := resumeTaskIDFromMetadata(task.Metadata)
	if resumeTaskID == "" {
		return nil, nil
	}
	resumeCtx := contextWithResumeScope(ctx, task.ProjectID, task.TenantID)
	if err := w.validateResumeProject(resumeCtx, resumeTaskID); err != nil {
		return nil, err
	}

	var (
		meta map[string]any
		err  error
	)
	if w.driverResumer != nil {
		meta, err = w.driverResumer.ResumeFromTask(resumeCtx, resumeTaskID)
	} else {
		meta, err = w.ResumeFromTask(resumeCtx, resumeTaskID)
	}
	if err != nil {
		return nil, err
	}
	if task.Metadata == nil {
		task.Metadata = map[string]any{}
	}
	for key, value := range meta {
		task.Metadata[key] = value
	}
	task.Metadata["resume_id"] = resumeTaskID
	return meta, nil
}

func (w *CodeWorker) validateResumeProject(ctx context.Context, resumeTaskID string) error {
	if w.loom == nil {
		return types.NewCapabilityMismatch("code worker Loom client is required for resume", nil)
	}
	prev, err := w.getTask(ctx, resumeTaskID)
	if err != nil {
		return types.NewUserInputError(fmt.Sprintf("resume task %q not found", resumeTaskID), err)
	}
	return validateResumeProject(ctx, prev)
}

func (w *CodeWorker) getTask(ctx context.Context, taskID string) (*loom.Task, error) {
	if getter, ok := w.loom.(contextTaskGetter); ok {
		return getter.GetContext(ctx, taskID)
	}
	return w.loom.Get(taskID)
}

func (w *CodeWorker) driverCLIForTask(task *loom.Task) types.CLIName {
	if task != nil {
		if cli := strings.TrimSpace(task.CLI); cli != "" {
			return types.CLIName(cli)
		}
		if cli, ok := metadataString(task.Metadata, "driver_cli_override"); ok && strings.TrimSpace(cli) != "" {
			return types.CLIName(strings.TrimSpace(cli))
		}
	}
	return w.driverCLI
}

func (w *CodeWorker) pairCLIsForTask(ctx context.Context, task *loom.Task) (types.CLIName, types.CLIName, error) {
	driverCLI := w.driverCLIForTask(task)
	if taskHasDriverOverride(task) {
		if w.navigatorCLI != "" {
			return driverCLI, w.navigatorCLI, nil
		}
		if selector, ok := w.pairSelector.(driverPairSelector); ok {
			return selector.PickPairForDriver(ctx, "code", driverCLI)
		}
		return driverCLI, w.defaultNavigatorCLI(), nil
	}
	if w.navigatorCLI != "" {
		return driverCLI, w.navigatorCLI, nil
	}
	if w.pairSelector != nil {
		return w.pairSelector.PickPair(ctx, "code")
	}
	return driverCLI, w.defaultNavigatorCLI(), nil
}

func (w *CodeWorker) defaultNavigatorCLI() types.CLIName {
	if w.navigatorCLI != "" {
		return w.navigatorCLI
	}
	return "claude"
}

func taskHasDriverOverride(task *loom.Task) bool {
	if task == nil {
		return false
	}
	if strings.TrimSpace(task.CLI) != "" {
		return true
	}
	if cli, ok := metadataString(task.Metadata, "driver_cli_override"); ok && strings.TrimSpace(cli) != "" {
		return true
	}
	return false
}

func pairTaskTimeout(task *loom.Task) time.Duration {
	if task == nil || task.Timeout <= 0 {
		return 0
	}
	return time.Duration(task.Timeout) * time.Second
}

func recordSelectedPair(task *loom.Task, driverCLI, navigatorCLI types.CLIName) {
	if task.Metadata == nil {
		task.Metadata = map[string]any{}
	}
	task.Metadata["driver_cli"] = string(driverCLI)
	task.Metadata["navigator_cli"] = string(navigatorCLI)
}

func resumeTaskIDFromMetadata(metadata map[string]any) string {
	for _, key := range []string{"resume_id", MetadataResumeTaskID} {
		if value, ok := metadataString(metadata, key); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func ensureCLIError(err error) *types.CLIError {
	if err == nil {
		return types.NewUnknown("code worker failed without error detail", nil)
	}
	var cliErr *types.CLIError
	if errors.As(err, &cliErr) {
		return cliErr
	}
	return types.NewUnknown(err.Error(), err)
}

func (w *CodeWorker) recordTaskMetadata(task *loom.Task, machine *Machine, criteria SuccessCriteria, verdict Verdict, gateResult string) {
	if task.Metadata == nil {
		task.Metadata = map[string]any{}
	}
	task.Metadata[MetadataWorkerType] = string(WorkerTypeCode)
	if _, ok := metadataString(task.Metadata, "driver_cli"); !ok {
		task.Metadata["driver_cli"] = string(w.driverCLIForTask(task))
	}
	if _, ok := metadataString(task.Metadata, "navigator_cli"); !ok {
		task.Metadata["navigator_cli"] = string(w.defaultNavigatorCLI())
	}
	rounds := machine.Rounds()
	if verdict.Action != "" {
		rounds++
	}
	task.Metadata["rounds"] = rounds
	if verdict.Action != "" {
		task.Metadata["confidence_score"] = verdict.Confidence
		task.Metadata["verdict"] = string(verdict.Action)
	}
	if strings.TrimSpace(verdict.ThreadID) != "" {
		task.Metadata[MetadataThreadID] = strings.TrimSpace(verdict.ThreadID)
	}
	if gateResult != "" {
		task.Metadata["gate_result"] = gateResult
	}
	task.Metadata["success_criteria"] = successCriteriaMetadata(criteria)
	for key, value := range machine.Metadata() {
		task.Metadata[key] = value
	}
}

func successCriteriaMetadata(criteria SuccessCriteria) []map[string]any {
	active := criteria.NormalizeWeights().Criteria()
	out := make([]map[string]any, 0, len(active))
	for _, criterion := range active {
		out = append(out, map[string]any{
			"name":        criterion.Name,
			"description": criterion.Description,
			"weight":      criterion.Weight,
		})
	}
	return out
}

func promptWithFeedback(original string, feedback string) string {
	feedback = strings.TrimSpace(feedback)
	if feedback == "" {
		return original
	}
	return original + "\n\nNavigator feedback:\n" + feedback
}

func verdictEvidence(verdict Verdict) string {
	for _, value := range []string{verdict.Evidence, verdict.Feedback} {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return "navigator requested escalation"
}
