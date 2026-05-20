package code

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// ExecGitDiffer implements GitDiffer using the git binary.
type ExecGitDiffer struct{}

func (ExecGitDiffer) Diff(ctx context.Context, cwd string) (string, error) {
	// Mark new untracked files as intent-to-add so git diff includes them.
	addNew := exec.CommandContext(ctx, "git", "add", "-N", ".")
	addNew.Dir = cwd
	_ = addNew.Run() // best-effort; some repos may have hooks that reject

	cmd := exec.CommandContext(ctx, "git", "diff", "--no-color")
	cmd.Dir = cwd
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git diff: %w", err)
	}
	staged := exec.CommandContext(ctx, "git", "diff", "--cached", "--no-color")
	staged.Dir = cwd
	stagedOut, err := staged.Output()
	if err != nil {
		return string(out), nil
	}
	combined := strings.TrimSpace(string(out)) + "\n" + strings.TrimSpace(string(stagedOut))
	return strings.TrimSpace(combined), nil
}

func (ExecGitDiffer) Revert(ctx context.Context, cwd string) error {
	checkout := exec.CommandContext(ctx, "git", "checkout", "--", ".")
	checkout.Dir = cwd
	if err := checkout.Run(); err != nil {
		return fmt.Errorf("git checkout: %w", err)
	}
	clean := exec.CommandContext(ctx, "git", "clean", "-fd")
	clean.Dir = cwd
	if err := clean.Run(); err != nil {
		return fmt.Errorf("git clean: %w", err)
	}
	return nil
}
