package code

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/thebtf/aimux/pkg/executor/types"
)

var hunkHeaderRE = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)

type filePatch struct {
	path  string
	hunks []hunkPatch
}

type hunkPatch struct {
	oldStart int
	lines    []string
}

type plannedWrite struct {
	path    string
	content []byte
}

type fileBackup struct {
	content []byte
	existed bool
}

// WriteDiff applies a unified diff to project.CWD atomically for parse/validation failures.
func WriteDiff(ctx context.Context, diff string, project Project) (int, int, error) {
	if err := ctx.Err(); err != nil {
		return 0, 0, err
	}
	root, err := normalizeProjectRoot(project.CWD)
	if err != nil {
		return 0, 0, err
	}
	patches, err := parseUnifiedDiff(diff)
	if err != nil {
		return 0, 0, err
	}

	writes := make([]plannedWrite, 0, len(patches))
	seen := map[string]struct{}{}
	hunksApplied := 0
	for _, patch := range patches {
		if err := ctx.Err(); err != nil {
			return 0, 0, err
		}
		target, err := resolvePatchTarget(root, patch.path)
		if err != nil {
			return 0, 0, err
		}
		if _, ok := seen[target]; ok {
			return 0, 0, fmt.Errorf("duplicate file patch for %s", patch.path)
		}
		seen[target] = struct{}{}

		original, err := os.ReadFile(target)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return 0, 0, fmt.Errorf("read patch target %s: %w", patch.path, err)
		}
		next, err := applyFilePatch(original, patch)
		if err != nil {
			return 0, 0, fmt.Errorf("apply patch %s: %w", patch.path, err)
		}
		writes = append(writes, plannedWrite{path: target, content: next})
		hunksApplied += len(patch.hunks)
	}

	if err := writePlannedFiles(ctx, writes); err != nil {
		return 0, 0, err
	}
	return len(writes), hunksApplied, nil
}

func parseUnifiedDiff(diff string) ([]filePatch, error) {
	lines := strings.Split(strings.ReplaceAll(diff, "\r\n", "\n"), "\n")
	patches := make([]filePatch, 0)
	for i := 0; i < len(lines); {
		line := lines[i]
		if isDiffMetadataLine(line) || line == "" {
			i++
			continue
		}
		if !strings.HasPrefix(line, "--- ") {
			return nil, fmt.Errorf("expected file header, got %q", line)
		}
		if i+1 >= len(lines) || !strings.HasPrefix(lines[i+1], "+++ ") {
			return nil, fmt.Errorf("missing +++ header after %q", line)
		}
		path, err := parsePatchPath(lines[i+1])
		if err != nil {
			return nil, err
		}
		i += 2

		patch := filePatch{path: path}
		for i < len(lines) {
			if lines[i] == "" && i == len(lines)-1 {
				break
			}
			if strings.HasPrefix(lines[i], "diff --git ") || strings.HasPrefix(lines[i], "--- ") {
				break
			}
			if !strings.HasPrefix(lines[i], "@@ ") {
				return nil, fmt.Errorf("expected hunk header for %s, got %q", path, lines[i])
			}
			hunk, next, err := parseHunk(lines, i)
			if err != nil {
				return nil, err
			}
			patch.hunks = append(patch.hunks, hunk)
			i = next
		}
		if len(patch.hunks) == 0 {
			return nil, fmt.Errorf("file patch %s has no hunks", path)
		}
		patches = append(patches, patch)
	}
	if len(patches) == 0 {
		return nil, errors.New("diff contains no file patches")
	}
	return patches, nil
}

func isDiffMetadataLine(line string) bool {
	return strings.HasPrefix(line, "diff --git ") ||
		strings.HasPrefix(line, "index ") ||
		strings.HasPrefix(line, "new file mode ") ||
		strings.HasPrefix(line, "deleted file mode ") ||
		strings.HasPrefix(line, "similarity index ") ||
		strings.HasPrefix(line, "rename from ") ||
		strings.HasPrefix(line, "rename to ")
}

func parsePatchPath(header string) (string, error) {
	path := strings.TrimSpace(strings.TrimPrefix(header, "+++ "))
	if path == "/dev/null" {
		return "", errors.New("delete-file patches are not supported")
	}
	if path == "" {
		return "", errors.New("empty patch target path")
	}
	if strings.HasPrefix(path, "a/") || strings.HasPrefix(path, "b/") {
		return path[2:], nil
	}
	return path, nil
}

func parseHunk(lines []string, start int) (hunkPatch, int, error) {
	match := hunkHeaderRE.FindStringSubmatch(lines[start])
	if match == nil {
		return hunkPatch{}, start, fmt.Errorf("invalid hunk header %q", lines[start])
	}
	oldStart, err := strconv.Atoi(match[1])
	if err != nil {
		return hunkPatch{}, start, fmt.Errorf("parse hunk old start: %w", err)
	}
	hunk := hunkPatch{oldStart: oldStart}
	i := start + 1
	for i < len(lines) {
		line := lines[i]
		if line == "" && i == len(lines)-1 {
			break
		}
		if strings.HasPrefix(line, "@@ ") || strings.HasPrefix(line, "diff --git ") || strings.HasPrefix(line, "--- ") {
			break
		}
		if strings.HasPrefix(line, `\ No newline at end of file`) {
			i++
			continue
		}
		if line == "" {
			return hunkPatch{}, start, fmt.Errorf("invalid empty hunk line after %q", lines[start])
		}
		switch line[0] {
		case ' ', '+', '-':
			hunk.lines = append(hunk.lines, line)
		default:
			return hunkPatch{}, start, fmt.Errorf("invalid hunk line %q", line)
		}
		i++
	}
	if len(hunk.lines) == 0 {
		return hunkPatch{}, start, fmt.Errorf("hunk %q has no body", lines[start])
	}
	return hunk, i, nil
}

func applyFilePatch(original []byte, patch filePatch) ([]byte, error) {
	originalText := strings.ReplaceAll(string(original), "\r\n", "\n")
	originalLines := splitPatchLines(originalText)
	out := make([]string, 0, len(originalLines))
	pos := 0

	for _, hunk := range patch.hunks {
		target := hunk.oldStart - 1
		if hunk.oldStart == 0 {
			target = 0
		}
		if target < pos || target > len(originalLines) {
			return nil, fmt.Errorf("hunk target line %d outside file", hunk.oldStart)
		}
		out = append(out, originalLines[pos:target]...)
		pos = target

		for _, line := range hunk.lines {
			text := line[1:]
			switch line[0] {
			case ' ':
				if pos >= len(originalLines) || originalLines[pos] != text {
					return nil, fmt.Errorf("context mismatch at line %d: want %q", pos+1, text)
				}
				out = append(out, text)
				pos++
			case '-':
				if pos >= len(originalLines) || originalLines[pos] != text {
					return nil, fmt.Errorf("remove mismatch at line %d: want %q", pos+1, text)
				}
				pos++
			case '+':
				out = append(out, text)
			}
		}
	}
	out = append(out, originalLines[pos:]...)
	return []byte(joinPatchLines(out, shouldEndWithNewline(originalText, out))), nil
}

func splitPatchLines(text string) []string {
	if text == "" {
		return []string{}
	}
	lines := strings.Split(text, "\n")
	if strings.HasSuffix(text, "\n") {
		return lines[:len(lines)-1]
	}
	return lines
}

func joinPatchLines(lines []string, trailingNewline bool) string {
	if len(lines) == 0 {
		return ""
	}
	joined := strings.Join(lines, "\n")
	if trailingNewline {
		return joined + "\n"
	}
	return joined
}

func shouldEndWithNewline(original string, next []string) bool {
	if original == "" {
		return len(next) > 0
	}
	return strings.HasSuffix(original, "\n")
}

func normalizeProjectRoot(cwd string) (string, error) {
	if cwd == "" {
		return "", errors.New("project cwd is required")
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
		return "", fmt.Errorf("stat project root: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("project root is not a directory: %s", real)
	}
	return real, nil
}

func resolvePatchTarget(root string, patchPath string) (string, error) {
	patchPath = filepath.FromSlash(strings.TrimSpace(patchPath))
	if filepath.IsAbs(patchPath) {
		return "", pathEscapesWorktreeError(patchPath)
	}
	clean := filepath.Clean(patchPath)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", pathEscapesWorktreeError(patchPath)
	}
	target := filepath.Join(root, clean)
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return "", fmt.Errorf("resolve patch target: %w", err)
	}
	if err := ensurePatchTargetInsideRoot(root, absTarget); err != nil {
		return "", err
	}
	return absTarget, nil
}

func ensurePatchTargetInsideRoot(root string, absTarget string) error {
	if err := ensureInsideRoot(root, absTarget); err != nil {
		return err
	}
	if realTarget, err := filepath.EvalSymlinks(absTarget); err == nil {
		if err := ensureInsideRoot(root, realTarget); err != nil {
			return err
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("resolve patch target realpath: %w", err)
	}
	return ensureExistingAncestorInsideRoot(root, filepath.Dir(absTarget))
}

func ensureExistingAncestorInsideRoot(root string, path string) error {
	for {
		real, err := filepath.EvalSymlinks(path)
		if err == nil {
			return ensureInsideRoot(root, real)
		}
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("resolve patch parent realpath: %w", err)
		}
		next := filepath.Dir(path)
		if next == path {
			return fmt.Errorf("resolve patch parent realpath: %w", err)
		}
		path = next
	}
}

func ensureInsideRoot(root string, target string) error {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return fmt.Errorf("compare path to project root: %w", err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return pathEscapesWorktreeError(target)
	}
	return nil
}

func pathEscapesWorktreeError(path string) *types.CLIError {
	return types.NewSandboxDenial("path escapes worktree root: "+path, nil)
}

func writePlannedFiles(ctx context.Context, writes []plannedWrite) error {
	backups := make(map[string]fileBackup, len(writes))
	for _, write := range writes {
		if err := ctx.Err(); err != nil {
			rollbackWrites(backups)
			return err
		}
		content, err := os.ReadFile(write.path)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				rollbackWrites(backups)
				return fmt.Errorf("backup %s: %w", write.path, err)
			}
			backups[write.path] = fileBackup{existed: false}
		} else {
			backups[write.path] = fileBackup{content: content, existed: true}
		}
		if err := os.MkdirAll(filepath.Dir(write.path), 0o755); err != nil {
			rollbackWrites(backups)
			return fmt.Errorf("create patch target dir: %w", err)
		}
		if err := os.WriteFile(write.path, write.content, 0o644); err != nil {
			rollbackWrites(backups)
			return fmt.Errorf("write patch target %s: %w", write.path, err)
		}
	}
	return nil
}

func rollbackWrites(backups map[string]fileBackup) {
	for path, backup := range backups {
		if backup.existed {
			_ = os.WriteFile(path, backup.content, 0o644)
			continue
		}
		_ = os.Remove(path)
	}
}
