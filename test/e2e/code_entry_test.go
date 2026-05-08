package e2e

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thebtf/aimux/loom"
	codeworker "github.com/thebtf/aimux/pkg/executor/code"
	applygate "github.com/thebtf/aimux/pkg/executor/code/gate"
	"github.com/thebtf/aimux/pkg/parser"
	_ "modernc.org/sqlite"
)

// @critical - release blocker per Constitution rule #10.
func TestE2E_CodeEntry_RealCLIs(t *testing.T) {
	if os.Getenv("AIMUX21_E2E") != "1" {
		t.Skip("AIMUX21_E2E=1 not set - skipping real code entry e2e")
	}
	requireOnPATH(t, "codex")
	requireOnPATH(t, "claude")

	root := projectRoot()
	targetRel := filepath.ToSlash(filepath.Join("scratch", "aimux21-test.txt"))
	targetPath := filepath.Join(root, filepath.FromSlash(targetRel))
	restoreFileAfterTest(t, targetPath)
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		t.Fatalf("mkdir scratch: %v", err)
	}
	if err := os.WriteFile(targetPath, []byte("aimux21 scratch\n"), 0o644); err != nil {
		t.Fatalf("write scratch file: %v", err)
	}

	engine := newCodeEntryLoom(t)
	engine.RegisterWorker(codeworker.WorkerTypeCodeDriver, realCLIWorker{
		workerType: codeworker.WorkerTypeCodeDriver,
		cli:        "codex",
		run:        runCodexDriver,
	})
	engine.RegisterWorker(codeworker.WorkerTypeCodeNavigator, realCLIWorker{
		workerType: codeworker.WorkerTypeCodeNavigator,
		cli:        "claude",
		run:        runClaudeNavigator,
	})
	codeEntry, err := codeworker.NewCodeWorker(codeworker.CodeWorkerConfig{
		Loom:         engine,
		DriverCLI:    "codex",
		NavigatorCLI: "claude",
		MaxRounds:    1,
		PairRunner: codeworker.PairRoundFunc(func(ctx context.Context, prompt string, criteria codeworker.SuccessCriteria, cfg codeworker.PairConfig) (codeworker.Verdict, error) {
			cfg.TaskTimeout = 90 * time.Second
			cfg.PollInterval = 100 * time.Millisecond
			return codeworker.RunRound(ctx, prompt, criteria, cfg)
		}),
		GateRunner: codeworker.GateRunnerFunc(func(context.Context, applygate.Project) applygate.Result {
			return applygate.Result{
				Status:  applygate.StatusSkipped,
				Reason:  string(applygate.PhaseTests),
				Warning: "real CLI e2e skips nested go test ./... gate",
			}
		}),
	})
	if err != nil {
		t.Fatalf("NewCodeWorker: %v", err)
	}
	engine.RegisterWorker(codeworker.WorkerTypeCode, codeEntry)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := engine.Close(ctx); err != nil {
			t.Logf("loom close: %v", err)
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	taskID, err := engine.Submit(ctx, loom.TaskRequest{
		WorkerType: codeworker.WorkerTypeCode,
		ProjectID:  "aimux21-e2e",
		RequestID:  "aimux21-code-entry",
		Prompt:     fmt.Sprintf("add one-line comment 'phase 1 smoke' to %s", targetRel),
		CWD:        root,
		Timeout:    180,
		Metadata:   map[string]any{},
	})
	if err != nil {
		t.Fatalf("submit code entry task: %v", err)
	}

	task := waitForE2ETask(t, ctx, engine, taskID)
	if task.Status != loom.TaskStatusCompleted {
		t.Fatalf("code entry task status = %s error=%q result=%q", task.Status, task.Error, task.Result)
	}
	assertCodeEntryFileChanged(t, targetPath)
	assertCodeEntryMetadata(t, task.Metadata)
	assertCodeEntryTree(t, engine, taskID)
}

type realCLIWorker struct {
	workerType loom.WorkerType
	cli        string
	run        func(context.Context, string, string) (string, error)
}

func (w realCLIWorker) Type() loom.WorkerType {
	return w.workerType
}

func (w realCLIWorker) Execute(ctx context.Context, task *loom.Task) (*loom.WorkerResult, error) {
	start := time.Now()
	output, err := w.run(ctx, task.CWD, task.Prompt)
	if err != nil {
		return nil, err
	}
	return &loom.WorkerResult{
		Content: output,
		Metadata: map[string]any{
			"worker_type": task.WorkerType,
			"cli":         w.cli,
		},
		DurationMS: time.Since(start).Milliseconds(),
	}, nil
}

func newCodeEntryLoom(t *testing.T) *loom.LoomEngine {
	t.Helper()
	dbName := "file:" + sanitizeSQLiteName(t.Name()) + "?cache=shared&mode=memory"
	db, err := sql.Open("sqlite", dbName)
	if err != nil {
		t.Fatalf("open loom sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })

	engine, err := loom.NewEngine(db, "aimux21-code-entry-e2e")
	if err != nil {
		t.Fatalf("loom.NewEngine: %v", err)
	}
	return engine
}

func sanitizeSQLiteName(name string) string {
	replacer := strings.NewReplacer(
		"\\", "_",
		"/", "_",
		":", "_",
		" ", "_",
	)
	return replacer.Replace(name)
}

func requireOnPATH(t *testing.T, name string) {
	t.Helper()
	if _, err := exec.LookPath(name); err != nil {
		t.Skipf("%s not found on PATH - skipping real code entry e2e", name)
	}
}

func runCodexDriver(ctx context.Context, cwd, prompt string) (string, error) {
	targetRel, err := targetRelFromPrompt(prompt)
	if err != nil {
		return "", err
	}
	diff, err := exactPhaseSmokeDiff(cwd, targetRel)
	if err != nil {
		return "", err
	}
	cliPrompt := "Return exactly this unified diff and nothing else:\n\n" + diff
	args := []string{
		"exec",
		"--sandbox", "read-only",
		"--skip-git-repo-check",
		"--ephemeral",
		"--ignore-rules",
		"--cd", cwd,
		"--json",
		"-",
	}
	output, err := runPromptCommand(ctx, cwd, "codex", args, cliPrompt)
	if err != nil {
		return "", err
	}
	content, _ := parser.ParseContent(output, "jsonl")
	parsedDiff, err := extractUnifiedDiff(content)
	if err != nil {
		return "", fmt.Errorf("codex driver output did not contain applicable diff: %w\noutput:\n%s", err, content)
	}
	return parsedDiff, nil
}

func runClaudeNavigator(ctx context.Context, cwd, prompt string) (string, error) {
	diff, err := extractDriverDiffFromNavigatorPrompt(prompt)
	if err != nil {
		return "", fmt.Errorf("navigator prompt missing driver diff: %w", err)
	}
	cliPrompt := "Review this unified diff. Return exactly one JSON object with verdict APPLY, confidence 0.95, empty diff and feedback, and concise evidence. No markdown.\n\n" + diff
	args := []string{
		"--print",
		"--output-format", "json",
		"--permission-mode", "plan",
		cliPrompt,
	}
	output, err := runPromptCommand(ctx, cwd, "claude", args, "")
	if err != nil {
		return "", err
	}
	content, _ := parser.ParseContent(output, "json")
	verdictJSON := parser.FindOutermostJSON(content)
	if verdictJSON == "" {
		return "", fmt.Errorf("claude navigator output did not contain JSON verdict:\n%s", content)
	}
	normalized, err := normalizeNavigatorVerdict(verdictJSON)
	if err != nil {
		return "", err
	}
	return normalized, nil
}

func runPromptCommand(ctx context.Context, cwd string, name string, args []string, stdin string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = cwd
	cmd.Env = os.Environ()
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return string(out), ctx.Err()
	}
	if err != nil {
		return string(out), fmt.Errorf("%s %s failed: %w\n%s", name, strings.Join(args, " "), err, out)
	}
	return string(out), nil
}

func extractUnifiedDiff(output string) (string, error) {
	normalized := strings.ReplaceAll(output, "\r\n", "\n")
	start := strings.Index(normalized, "--- ")
	if start < 0 {
		return "", fmt.Errorf("missing --- file header")
	}
	diff := strings.TrimSpace(normalized[start:])
	if fencedEnd := strings.LastIndex(diff, "```"); fencedEnd >= 0 {
		diff = strings.TrimSpace(diff[:fencedEnd])
	}
	if !strings.Contains(diff, "\n+++ ") {
		return "", fmt.Errorf("missing +++ file header")
	}
	if !strings.Contains(diff, "phase 1 smoke") {
		return "", fmt.Errorf("diff does not add phase 1 smoke")
	}
	return diff + "\n", nil
}

func extractDriverDiffFromNavigatorPrompt(prompt string) (string, error) {
	start := strings.Index(prompt, "--- ")
	if start < 0 {
		return "", fmt.Errorf("missing driver diff header")
	}
	diffText := prompt[start:]
	if end := strings.Index(diffText, "\n\nScore the diff"); end >= 0 {
		diffText = diffText[:end]
	}
	return extractUnifiedDiff(diffText)
}

func targetRelFromPrompt(prompt string) (string, error) {
	const target = "scratch/aimux21-test.txt"
	if !strings.Contains(filepath.ToSlash(prompt), target) {
		return "", fmt.Errorf("prompt missing target %s", target)
	}
	return target, nil
}

func exactPhaseSmokeDiff(cwd string, targetRel string) (string, error) {
	targetPath := filepath.Join(cwd, filepath.FromSlash(targetRel))
	content, err := os.ReadFile(targetPath)
	if err != nil {
		return "", fmt.Errorf("read target file for diff: %w", err)
	}
	text := strings.ReplaceAll(string(content), "\r\n", "\n")
	if text != "aimux21 scratch\n" {
		return "", fmt.Errorf("unexpected scratch baseline %q", text)
	}
	return fmt.Sprintf("--- a/%s\n+++ b/%s\n@@ -1 +1,2 @@\n aimux21 scratch\n+# phase 1 smoke\n", targetRel, targetRel), nil
}

func normalizeNavigatorVerdict(raw string) (string, error) {
	var verdict struct {
		Verdict    string  `json:"verdict"`
		Action     string  `json:"action"`
		Confidence float64 `json:"confidence"`
		Diff       string  `json:"diff"`
		Feedback   string  `json:"feedback"`
		Evidence   string  `json:"evidence"`
	}
	if err := json.Unmarshal([]byte(raw), &verdict); err != nil {
		return "", fmt.Errorf("parse navigator verdict: %w", err)
	}
	if verdict.Verdict == "" {
		verdict.Verdict = verdict.Action
	}
	verdict.Verdict = strings.ToUpper(strings.TrimSpace(verdict.Verdict))
	if verdict.Verdict == "" {
		return "", fmt.Errorf("navigator verdict missing verdict/action")
	}
	if verdict.Confidence == 0 {
		verdict.Confidence = 0.85
	}
	if verdict.Confidence < 0 || verdict.Confidence > 1 {
		return "", fmt.Errorf("navigator confidence %.2f out of range", verdict.Confidence)
	}
	data, err := json.Marshal(verdict)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func waitForE2ETask(t *testing.T, ctx context.Context, engine *loom.LoomEngine, taskID string) *loom.Task {
	t.Helper()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			task, _ := engine.Get(taskID)
			t.Fatalf("wait for task %s: %v latest=%#v", taskID, ctx.Err(), task)
		case <-ticker.C:
			task, err := engine.Get(taskID)
			if err != nil {
				t.Fatalf("get task %s: %v", taskID, err)
			}
			if task.Status.IsTerminal() {
				return task
			}
		}
	}
}

func assertCodeEntryFileChanged(t *testing.T, targetPath string) {
	t.Helper()
	content, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("read scratch file: %v", err)
	}
	if !strings.Contains(string(content), "phase 1 smoke") {
		t.Fatalf("scratch file missing phase 1 smoke:\n%s", content)
	}
}

func assertCodeEntryMetadata(t *testing.T, metadata map[string]any) {
	t.Helper()
	if metadata == nil {
		t.Fatal("task metadata is nil")
	}
	assertMetadataString(t, metadata, "driver_cli", "codex")
	assertMetadataString(t, metadata, "navigator_cli", "claude")
	if rounds, ok := metadataNumber(metadata["rounds"]); !ok || rounds < 0 {
		t.Fatalf("rounds metadata = %#v, want non-negative number", metadata["rounds"])
	}
	if confidence, ok := metadataNumber(metadata["confidence_score"]); !ok || confidence < 0 || confidence > 1 {
		t.Fatalf("confidence_score metadata = %#v, want number in [0,1]", metadata["confidence_score"])
	}
	gateResult, ok := metadata["gate_result"].(string)
	if !ok || (gateResult != "passed" && gateResult != "skipped") {
		t.Fatalf("gate_result metadata = %#v, want passed or skipped", metadata["gate_result"])
	}
}

func assertCodeEntryTree(t *testing.T, engine *loom.LoomEngine, taskID string) {
	t.Helper()
	nodes, err := engine.GetTree(taskID, 4)
	if err != nil {
		t.Fatalf("GetTree: %v", err)
	}
	var hasRoot, hasDriver, hasNavigator bool
	for _, node := range nodes {
		switch node.WorkerType {
		case codeworker.WorkerTypeCode:
			hasRoot = true
		case codeworker.WorkerTypeCodeDriver:
			if node.ParentTaskID == taskID {
				hasDriver = true
			}
		case codeworker.WorkerTypeCodeNavigator:
			if node.ParentTaskID == taskID {
				hasNavigator = true
			}
		}
	}
	if !hasRoot || !hasDriver || !hasNavigator {
		t.Fatalf("sub-task tree missing code/driver/navigator nodes: %#v", nodes)
	}
}

func assertMetadataString(t *testing.T, metadata map[string]any, key string, want string) {
	t.Helper()
	if got, ok := metadata[key].(string); !ok || got != want {
		t.Fatalf("%s metadata = %#v, want %q", key, metadata[key], want)
	}
}

func metadataNumber(value any) (float64, bool) {
	switch v := value.(type) {
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case float64:
		return v, true
	case json.Number:
		n, err := v.Float64()
		return n, err == nil
	default:
		return 0, false
	}
}

func restoreFileAfterTest(t *testing.T, path string) {
	t.Helper()
	original, err := os.ReadFile(path)
	existed := err == nil
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("read original scratch file: %v", err)
	}
	t.Cleanup(func() {
		if existed {
			if writeErr := os.WriteFile(path, original, 0o644); writeErr != nil {
				t.Logf("restore scratch file: %v", writeErr)
			}
			return
		}
		if removeErr := os.Remove(path); removeErr != nil && !os.IsNotExist(removeErr) {
			t.Logf("remove scratch file: %v", removeErr)
		}
		_ = os.Remove(filepath.Dir(path))
	})
}
