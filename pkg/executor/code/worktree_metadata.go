package code

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/thebtf/aimux/loom"
)

const (
	MetadataWorktreePath           = "worktree_path"
	MetadataWorktreeBranch         = "worktree_branch"
	MetadataWorktreeBaseSHA        = "worktree_base_sha"
	MetadataWorktreePreserveReason = "worktree_preserve_reason"

	WorktreePreserveReasonMutatingTask = "code task mutates caller worktree"
)

const unknownWorktreeMetadata = "unknown"

func recordWorktreePreservationMetadata(ctx context.Context, task *loom.Task, reason string) {
	if task == nil {
		return
	}
	if task.Metadata == nil {
		task.Metadata = map[string]any{}
	}
	for key, value := range worktreePreservationMetadata(ctx, task.CWD, reason) {
		task.Metadata[key] = value
	}
}

func worktreePreservationMetadata(ctx context.Context, cwd string, reason string) map[string]any {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = WorktreePreserveReasonMutatingTask
	}
	path := cleanWorktreePath(cwd)
	if topLevel, ok := gitCommandOutput(ctx, cwd, "rev-parse", "--show-toplevel"); ok && strings.TrimSpace(topLevel) != "" {
		path = filepath.Clean(strings.TrimSpace(topLevel))
	}

	branch := unknownWorktreeMetadata
	if current, ok := gitCommandOutput(ctx, cwd, "branch", "--show-current"); ok && strings.TrimSpace(current) != "" {
		branch = strings.TrimSpace(current)
	} else if current, ok := gitCommandOutput(ctx, cwd, "rev-parse", "--abbrev-ref", "HEAD"); ok {
		current = strings.TrimSpace(current)
		if current != "" && current != "HEAD" {
			branch = current
		}
	}

	baseSHA := unknownWorktreeMetadata
	if head, ok := gitCommandOutput(ctx, cwd, "rev-parse", "HEAD"); ok && strings.TrimSpace(head) != "" {
		baseSHA = strings.TrimSpace(head)
	}

	return map[string]any{
		MetadataWorktreePath:           path,
		MetadataWorktreeBranch:         branch,
		MetadataWorktreeBaseSHA:        baseSHA,
		MetadataWorktreePreserveReason: reason,
	}
}

func cleanWorktreePath(cwd string) string {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		cwd = "."
	}
	return filepath.Clean(cwd)
}

func gitCommandOutput(ctx context.Context, cwd string, args ...string) (string, bool) {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		cwd = "."
	}
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", cwd}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(out)), true
}
