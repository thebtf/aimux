// Package gate runs post-apply verification phases for code tasks.
package gate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const DefaultPhaseTimeout = 5 * time.Minute

// Status is the outcome of a gate or phase.
type Status string

const (
	StatusPassed  Status = "passed"
	StatusFailed  Status = "failed"
	StatusSkipped Status = "skipped"
)

// Phase names the fixed gate execution phases.
type Phase string

const (
	PhaseBuild     Phase = "build"
	PhaseTypeCheck Phase = "typecheck"
	PhaseTests     Phase = "tests"
	PhaseDetect    Phase = "detect"
)

// Project describes the directory to verify.
type Project struct {
	CWD          string
	PhaseTimeout time.Duration
}

// Result is the aggregate gate outcome.
type Result struct {
	Status      Status
	Reason      string
	Warning     string
	ProjectType ProjectType
	Phases      []PhaseResult
}

// PhaseResult is the per-phase status and log.
type PhaseResult struct {
	Name   Phase
	Status Status
	Log    string
}

type commandSpec struct {
	name string
	args []string
}

// Run executes build, typecheck, and tests in order.
func Run(ctx context.Context, project Project) Result {
	cwd, err := normalizeCWD(project.CWD)
	if err != nil {
		return Result{
			Status: StatusFailed,
			Reason: string(PhaseDetect),
			Phases: []PhaseResult{{
				Name:   PhaseDetect,
				Status: StatusFailed,
				Log:    err.Error(),
			}},
		}
	}

	projectType, err := DetectProjectType(cwd)
	if err != nil {
		return Result{
			Status: StatusFailed,
			Reason: string(PhaseDetect),
			Phases: []PhaseResult{{
				Name:   PhaseDetect,
				Status: StatusFailed,
				Log:    err.Error(),
			}},
		}
	}
	if projectType == ProjectTypeUnknown {
		return Result{
			Status:      StatusFailed,
			Reason:      string(PhaseDetect),
			ProjectType: projectType,
			Phases: []PhaseResult{{
				Name:   PhaseDetect,
				Status: StatusFailed,
				Log:    "no supported project marker found",
			}},
		}
	}

	timeout := phaseTimeout(project)
	result := Result{
		Status:      StatusPassed,
		ProjectType: projectType,
		Phases:      make([]PhaseResult, 0, 3),
	}

	for _, phase := range []Phase{PhaseBuild, PhaseTypeCheck, PhaseTests} {
		if phase == PhaseTests {
			hasTests, err := HasTests(cwd, projectType)
			if err != nil {
				phaseResult := PhaseResult{Name: phase, Status: StatusFailed, Log: err.Error()}
				result.Phases = append(result.Phases, phaseResult)
				result.Status = StatusFailed
				result.Reason = string(phase)
				return result
			}
			if !hasTests {
				warning := "tests skipped: no test files detected"
				result.Phases = append(result.Phases, PhaseResult{Name: phase, Status: StatusSkipped, Log: warning})
				result.Status = StatusSkipped
				result.Reason = string(phase)
				result.Warning = warning
				return result
			}
		}

		phaseResult := runPhase(ctx, cwd, projectType, phase, timeout)
		result.Phases = append(result.Phases, phaseResult)
		if phaseResult.Status == StatusFailed {
			result.Status = StatusFailed
			result.Reason = string(phase)
			return result
		}
	}

	return result
}

func runPhase(ctx context.Context, cwd string, projectType ProjectType, phase Phase, timeout time.Duration) PhaseResult {
	spec, ok := commandForPhase(projectType, phase)
	if !ok {
		return PhaseResult{
			Name:   phase,
			Status: StatusSkipped,
			Log:    fmt.Sprintf("%s phase unsupported for %s project", phase, projectType),
		}
	}
	return runCommand(ctx, cwd, phase, timeout, spec)
}

func commandForPhase(projectType ProjectType, phase Phase) (commandSpec, bool) {
	switch phase {
	case PhaseBuild:
		return buildCommand(projectType)
	case PhaseTypeCheck:
		return typeCheckCommand(projectType)
	case PhaseTests:
		return testsCommand(projectType)
	default:
		return commandSpec{}, false
	}
}

func runCommand(ctx context.Context, cwd string, phase Phase, timeout time.Duration, spec commandSpec) PhaseResult {
	phaseCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(phaseCtx, spec.name, spec.args...)
	cmd.Dir = cwd
	output, err := cmd.CombinedOutput()
	log := commandLog(spec, output, err)
	if errors.Is(phaseCtx.Err(), context.DeadlineExceeded) {
		return PhaseResult{
			Name:   phase,
			Status: StatusFailed,
			Log:    "timeout: " + log,
		}
	}
	if err != nil {
		return PhaseResult{
			Name:   phase,
			Status: StatusFailed,
			Log:    log,
		}
	}
	return PhaseResult{
		Name:   phase,
		Status: StatusPassed,
		Log:    log,
	}
}

func commandLog(spec commandSpec, output []byte, err error) string {
	parts := []string{commandLine(spec)}
	trimmed := strings.TrimSpace(string(output))
	if trimmed != "" {
		parts = append(parts, trimmed)
	}
	if err != nil {
		parts = append(parts, err.Error())
	} else if trimmed == "" {
		parts = append(parts, "passed")
	}
	return strings.Join(parts, "\n")
}

func commandLine(spec commandSpec) string {
	if len(spec.args) == 0 {
		return spec.name
	}
	return spec.name + " " + strings.Join(spec.args, " ")
}

func normalizeCWD(cwd string) (string, error) {
	if cwd == "" {
		current, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("resolve current directory: %w", err)
		}
		cwd = current
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return "", fmt.Errorf("resolve project cwd: %w", err)
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("resolve project realpath: %w", err)
	}
	info, err := os.Stat(real)
	if err != nil {
		return "", fmt.Errorf("stat project cwd: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("project cwd is not a directory: %s", real)
	}
	return real, nil
}

func phaseTimeout(project Project) time.Duration {
	if project.PhaseTimeout > 0 {
		return project.PhaseTimeout
	}
	return DefaultPhaseTimeout
}
