package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/thebtf/aimux/loom"
	"github.com/thebtf/aimux/pkg/executor/code"
	"github.com/thebtf/aimux/pkg/executor/picker"
	"github.com/thebtf/aimux/pkg/executor/review"
	extypes "github.com/thebtf/aimux/pkg/executor/types"
	"github.com/thebtf/aimux/pkg/parser"
	"github.com/thebtf/aimux/pkg/tenant"
)

type leafOutputAdapter func(task *loom.Task, parsed string) (string, map[string]any, error)

type profileTaskWorker struct {
	server     *Server
	workerType loom.WorkerType
	taskClass  string
	defaultCLI string
	adapt      leafOutputAdapter
}

type tenantAwareSubtaskLoom struct {
	engine   *loom.LoomEngine
	quotaFor func(tenantID string) *loom.TenantQuotaConfig
}

func (l tenantAwareSubtaskLoom) Submit(ctx context.Context, req loom.TaskRequest) (string, error) {
	if l.engine == nil {
		return "", extypes.NewCapabilityMismatch("tenant-aware subtask loom requires engine", nil)
	}
	if req.TenantID != "" {
		return loom.NewTenantScopedEngine(l.engine, req.TenantID, l.quota(req.TenantID)).Submit(ctx, req)
	}
	return l.engine.Submit(ctx, req)
}

func (l tenantAwareSubtaskLoom) Get(taskID string) (*loom.Task, error) {
	if l.engine == nil {
		return nil, extypes.NewCapabilityMismatch("tenant-aware subtask loom requires engine", nil)
	}
	return l.engine.Get(taskID)
}

func (l tenantAwareSubtaskLoom) GetContext(ctx context.Context, taskID string) (*loom.Task, error) {
	if l.engine == nil {
		return nil, extypes.NewCapabilityMismatch("tenant-aware subtask loom requires engine", nil)
	}
	if tc, ok := tenant.FromContext(ctx); ok && strings.TrimSpace(tc.TenantID) != "" {
		return loom.NewTenantScopedEngine(l.engine, tc.TenantID, l.quota(tc.TenantID)).GetContext(ctx, taskID)
	}
	return l.engine.GetContext(ctx, taskID)
}

func (l tenantAwareSubtaskLoom) Cancel(taskID string) error {
	if l.engine == nil {
		return extypes.NewCapabilityMismatch("tenant-aware subtask loom requires engine", nil)
	}
	return l.engine.Cancel(taskID)
}

func (l tenantAwareSubtaskLoom) quota(tenantID string) *loom.TenantQuotaConfig {
	if l.quotaFor == nil {
		return nil
	}
	return l.quotaFor(tenantID)
}

func (w profileTaskWorker) Type() loom.WorkerType {
	return w.workerType
}

func (w profileTaskWorker) Execute(ctx context.Context, task *loom.Task) (*loom.WorkerResult, error) {
	if task == nil {
		return nil, extypes.NewUserInputError("profile task worker task is nil", nil)
	}
	if w.server == nil || w.server.registry == nil {
		return nil, extypes.NewCapabilityMismatch("profile task worker requires server registry", nil)
	}
	cli := strings.TrimSpace(task.CLI)
	if cli == "" {
		cli = w.defaultCLI
	}
	if cli == "" {
		return nil, extypes.NewCapabilityMismatch(fmt.Sprintf("%s worker requires a CLI", w.workerType), nil)
	}
	profile, err := w.server.registry.Get(cli)
	if err != nil || profile == nil {
		if err == nil {
			err = fmt.Errorf("profile is nil")
		}
		return nil, extypes.NewBinaryNotFound(fmt.Sprintf("CLI %q profile unavailable: %v", cli, err), err)
	}

	raw, err := w.server.taskDispatch(ctx, cli, picker.TaskSpec{
		TaskClass: w.taskClass,
		Prompt:    task.Prompt,
		CWD:       task.CWD,
		Env:       cloneEnv(task.Env),
		Model:     task.Model,
		Effort:    task.Effort,
		Sandbox:   sandboxFromTaskMetadata(task.Metadata),
	})
	if err != nil {
		return nil, err
	}
	parsed, sessionID := parser.ParseContent(raw, profile.OutputFormat)
	content := parsed
	metadata := map[string]any{}
	if w.adapt != nil {
		content, metadata, err = w.adapt(task, parsed)
		if err != nil {
			return nil, err
		}
	}
	if strings.TrimSpace(content) == "" {
		return nil, extypes.NewUnknown(fmt.Sprintf("%s worker produced empty output", w.workerType), nil)
	}

	metadata["worker_type"] = string(w.workerType)
	metadata["cli"] = cli
	metadata["output_format"] = profile.OutputFormat
	if sessionID != "" {
		metadata["cli_session_id"] = sessionID
	}
	return &loom.WorkerResult{
		Content:  content,
		Metadata: metadata,
	}, nil
}

func sandboxFromTaskMetadata(metadata map[string]any) string {
	if sandbox, ok := metadataString(metadata, "sandbox"); ok {
		return strings.TrimSpace(sandbox)
	}
	return ""
}

func (s *Server) registerTaskWorkers() {
	if s == nil || s.loom == nil {
		return
	}
	subtaskLoom := tenantAwareSubtaskLoom{engine: s.loom}

	codeWorker, codeErr := code.NewCodeWorker(code.CodeWorkerConfig{
		Loom: subtaskLoom,
	})
	if codeErr != nil {
		s.log.Warn("task workers: code worker init failed: %v", codeErr)
	} else {
		s.loom.RegisterWorker(code.WorkerTypeCode, codeWorker)
		s.loom.RegisterWorker(code.WorkerTypeCodeDriver, profileTaskWorker{
			server:     s,
			workerType: code.WorkerTypeCodeDriver,
			taskClass:  "code",
			defaultCLI: "codex",
		})
		s.loom.RegisterWorker(code.WorkerTypeCodeNavigator, profileTaskWorker{
			server:     s,
			workerType: code.WorkerTypeCodeNavigator,
			taskClass:  "code",
			defaultCLI: "claude",
			adapt:      adaptNavigatorOutput,
		})
	}

	reviewWorker, reviewErr := review.NewReviewWorker(review.ReviewWorkerConfig{
		Loom:                  subtaskLoom,
		DefaultTimeoutSeconds: serverDefaultTimeoutSeconds(s),
	})
	if reviewErr != nil {
		s.log.Warn("task workers: review worker init failed: %v", reviewErr)
		return
	}
	s.loom.RegisterWorker(review.WorkerTypeReview, reviewWorker)
	for _, workerType := range []loom.WorkerType{
		review.WorkerTypeReviewStructural,
		review.WorkerTypeReviewBehavioural,
		review.WorkerTypeReviewAdversarial,
	} {
		s.loom.RegisterWorker(workerType, profileTaskWorker{
			server:     s,
			workerType: workerType,
			taskClass:  "review",
			defaultCLI: "codex",
			adapt:      adaptReviewPassOutput,
		})
	}
}

func serverDefaultTimeoutSeconds(s *Server) int {
	if s == nil || s.cfg == nil {
		return 0
	}
	return s.cfg.Server.DefaultTimeoutSeconds
}

func adaptReviewPassOutput(task *loom.Task, parsed string) (string, map[string]any, error) {
	trimmed := strings.TrimSpace(parsed)
	var passJSON struct {
		Findings []review.Finding `json:"findings"`
		Summary  string           `json:"summary"`
	}
	if err := json.Unmarshal([]byte(trimmed), &passJSON); err == nil && strings.TrimSpace(passJSON.Summary) != "" {
		return trimmed, map[string]any{}, nil
	}

	pass := reviewPassFromTask(task)
	summary := fmt.Sprintf("%s review completed with no structured findings", pass)
	if trimmed != "" {
		summary = fmt.Sprintf("%s review completed; CLI output captured (%d bytes)", pass, len(trimmed))
	}
	content, err := json.Marshal(map[string]any{
		"findings": []review.Finding{},
		"summary":  summary,
	})
	if err != nil {
		return "", nil, extypes.NewUnknown("review pass serialization failed", err)
	}
	return string(content), map[string]any{"review_pass": string(pass)}, nil
}

func adaptNavigatorOutput(_ *loom.Task, parsed string) (string, map[string]any, error) {
	trimmed := strings.TrimSpace(parsed)
	var verdict struct {
		Verdict    string  `json:"verdict"`
		Action     string  `json:"action"`
		Confidence float64 `json:"confidence"`
		Diff       string  `json:"diff"`
		Feedback   string  `json:"feedback"`
		Evidence   string  `json:"evidence"`
	}
	if err := json.Unmarshal([]byte(trimmed), &verdict); err == nil {
		action := strings.TrimSpace(verdict.Verdict)
		if action == "" {
			action = strings.TrimSpace(verdict.Action)
		}
		if action != "" {
			return trimmed, map[string]any{}, nil
		}
	}

	content, err := json.Marshal(map[string]any{
		"verdict":    "ESCALATE",
		"confidence": 0,
		"feedback":   "navigator output was not structured JSON",
		"evidence":   summarizeLeafOutput(trimmed),
	})
	if err != nil {
		return "", nil, extypes.NewUnknown("navigator verdict serialization failed", err)
	}
	return string(content), map[string]any{"navigator_output_normalized": true}, nil
}

func reviewPassFromTask(task *loom.Task) review.PassName {
	if task != nil {
		if pass, ok := metadataText(task.Metadata, "review_pass"); ok && strings.TrimSpace(pass) != "" {
			return review.PassName(strings.TrimSpace(pass))
		}
		switch task.WorkerType {
		case review.WorkerTypeReviewStructural:
			return review.PassStructural
		case review.WorkerTypeReviewBehavioural:
			return review.PassBehavioural
		case review.WorkerTypeReviewAdversarial:
			return review.PassAdversarial
		}
	}
	return "review"
}

func metadataText(metadata map[string]any, key string) (string, bool) {
	if metadata == nil {
		return "", false
	}
	value, ok := metadata[key]
	if !ok || value == nil {
		return "", false
	}
	return fmt.Sprint(value), true
}

func summarizeLeafOutput(output string) string {
	output = strings.TrimSpace(output)
	if output == "" {
		return "empty output"
	}
	const max = 240
	if len(output) <= max {
		return output
	}
	return output[:max] + "..."
}
