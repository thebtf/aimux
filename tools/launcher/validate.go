package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

type ScenarioStatus string

const (
	StatusPass    ScenarioStatus = "PASS"
	StatusBlocked ScenarioStatus = "BLOCKED"
	StatusFail    ScenarioStatus = "FAIL"
)

type ScenarioResult struct {
	Name     string
	Status   ScenarioStatus
	Command  string
	LogPath  string
	Evidence []string
	Blocker  string
	Error    string
}

type validateOptions struct {
	OutDir        string
	ConfigDir     string
	CLIScope      []string
	IncludeAPI    bool
	IncludeManual bool
	SyntheticOnly bool
	AllowBlocked  bool
	Timeout       time.Duration
}

type validateRun struct {
	StartedAt    time.Time
	ReportPath   string
	RunDir       string
	Results      []ScenarioResult
	ManualRecipe []string
}

func runValidate(args []string) int {
	fs := flag.NewFlagSet("launcher validate", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	outDir := fs.String("out", ".agent/reports", "directory for validation markdown reports")
	configDir := fs.String("config-dir", "config", "aimux config directory for real CLI scenarios")
	cliScope := fs.String("cli-scope", "codex,claude,gemini", "comma-separated real CLI scope")
	includeAPI := fs.Bool("include-api", true, "run API provider validation scenarios")
	includeManual := fs.Bool("include-manual", true, "include manual TUI validation recipe in the report")
	syntheticOnly := fs.Bool("synthetic-only", false, "run deterministic synthetic scenarios only")
	allowBlocked := fs.Bool("allow-blocked", false, "exit 0 when scenarios are BLOCKED and no scenario FAILs")
	timeout := fs.Duration("timeout", 30*time.Second, "per-scenario timeout")

	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *timeout <= 0 {
		fmt.Fprintln(os.Stderr, "launcher validate: --timeout must be positive")
		return 2
	}

	opts := validateOptions{
		OutDir:        *outDir,
		ConfigDir:     *configDir,
		CLIScope:      parseCSV(*cliScope),
		IncludeAPI:    *includeAPI,
		IncludeManual: *includeManual,
		SyntheticOnly: *syntheticOnly,
		AllowBlocked:  *allowBlocked,
		Timeout:       *timeout,
	}

	run, err := executeValidation(opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "launcher validate: %v\n", err)
		return 1
	}
	fmt.Fprintf(os.Stdout, "validation report: %s\n", run.ReportPath)
	return validationExitCode(run.Results, opts.AllowBlocked)
}

func executeValidation(opts validateOptions) (validateRun, error) {
	started := time.Now()
	if err := os.MkdirAll(opts.OutDir, 0o755); err != nil {
		return validateRun{}, fmt.Errorf("create output dir %q: %w", opts.OutDir, err)
	}
	runID := started.Format("20060102-150405")
	runDir := filepath.Join(opts.OutDir, "AIMUX-17-cr002-validation-logs-"+runID)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return validateRun{}, fmt.Errorf("create run dir %q: %w", runDir, err)
	}

	launcher, err := os.Executable()
	if err != nil {
		return validateRun{}, fmt.Errorf("resolve launcher executable: %w", err)
	}

	results := runSyntheticScenarios(opts, launcher, runDir)
	if !opts.SyntheticOnly {
		results = append(results, runRealCLIScenarios(opts, launcher, runDir)...)
		if opts.IncludeAPI {
			results = append(results, runAPIScenarios(opts, launcher, runDir)...)
		}
		results = append(results, runSessionScenarios(opts, launcher, runDir)...)
	}

	recipe := []string(nil)
	if opts.IncludeManual && !opts.SyntheticOnly {
		recipe = manualTUIRecipe(opts, runDir)
	}

	reportPath := filepath.Join(opts.OutDir, "AIMUX-17-cr002-validation-"+runID+".md")
	run := validateRun{StartedAt: started, ReportPath: reportPath, RunDir: runDir, Results: results, ManualRecipe: recipe}
	if err := writeValidationReport(run); err != nil {
		return validateRun{}, err
	}
	return run, nil
}

func writeValidationReport(run validateRun) error {
	var b strings.Builder
	fmt.Fprintf(&b, "# AIMUX-17 CR-002 launcher validation report\n\n")
	fmt.Fprintf(&b, "- Started: `%s`\n", run.StartedAt.Format(time.RFC3339))
	fmt.Fprintf(&b, "- Run directory: `%s`\n", run.RunDir)
	fmt.Fprintf(&b, "- Summary: PASS=%d BLOCKED=%d FAIL=%d\n\n", countByStatus(run.Results, StatusPass), countByStatus(run.Results, StatusBlocked), countByStatus(run.Results, StatusFail))
	fmt.Fprintf(&b, "## Scenario summary\n\n| Scenario | Status | Command | Log | Blocker |\n|---|---:|---|---|---|\n")
	for _, r := range run.Results {
		fmt.Fprintf(&b, "| %s | %s | `%s` | `%s` | %s |\n", mdEscape(r.Name), r.Status, mdEscape(r.Command), mdEscape(r.LogPath), mdEscape(emptyDash(r.Blocker)))
	}
	fmt.Fprintf(&b, "\n## Scenario evidence\n")
	for _, r := range run.Results {
		fmt.Fprintf(&b, "\n### %s — %s\n\n", r.Name, r.Status)
		fmt.Fprintf(&b, "- Command: `%s`\n", r.Command)
		if r.LogPath != "" {
			fmt.Fprintf(&b, "- Log: `%s`\n", r.LogPath)
		}
		if r.Blocker != "" {
			fmt.Fprintf(&b, "- Blocker: %s\n", r.Blocker)
		}
		if r.Error != "" {
			fmt.Fprintf(&b, "- Error: %s\n", r.Error)
		}
		for _, e := range r.Evidence {
			fmt.Fprintf(&b, "- Evidence: %s\n", e)
		}
	}
	if len(run.ManualRecipe) > 0 {
		fmt.Fprintf(&b, "\n## Manual TUI validation recipe\n\n")
		for _, line := range run.ManualRecipe {
			fmt.Fprintf(&b, "%s\n", line)
		}
	}
	return os.WriteFile(run.ReportPath, []byte(b.String()), 0o644)
}

func runLauncher(ctx context.Context, launcher string, args []string, stdin string) (string, string, int, error) {
	cmd := exec.CommandContext(ctx, launcher, args...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			err = fmt.Errorf("%w: %v", ctxErr, err)
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			code = exitErr.ExitCode()
		} else {
			code = -1
		}
	}
	return stdout.String(), stderr.String(), code, err
}

func parseCSV(value string) []string {
	var out []string
	for _, item := range strings.Split(value, ",") {
		trimmed := strings.TrimSpace(item)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func countByStatus(results []ScenarioResult, status ScenarioStatus) int {
	count := 0
	for _, r := range results {
		if r.Status == status {
			count++
		}
	}
	return count
}

func validationExitCode(results []ScenarioResult, allowBlocked bool) int {
	if countByStatus(results, StatusFail) > 0 {
		return 1
	}
	if !allowBlocked && countByStatus(results, StatusBlocked) > 0 {
		return 2
	}
	return 0
}

func mdEscape(value string) string {
	value = strings.ReplaceAll(value, "|", "\\|")
	value = strings.ReplaceAll(value, "\n", " ")
	return value
}

func emptyDash(value string) string {
	if value == "" {
		return "-"
	}
	return value
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func memoryEvidenceUnavailable() string {
	return "peak process memory unavailable on " + runtime.GOOS + "; large-output memory-bound proof is BLOCKED for this scenario"
}

func memoryEvidence(measurement processMeasurement) string {
	return fmt.Sprintf("peak process memory bytes=%d method=%s", measurement.PeakBytes, measurement.Method)
}
