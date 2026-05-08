package code

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thebtf/aimux/pkg/executor/types"
)

func TestWriteDiffAbsolutePathRejectedWithSandboxDenial(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.txt")

	diff := strings.ReplaceAll(`--- a/file.txt
+++ ABSOLUTE
@@ -0,0 +1 @@
+owned
`, "ABSOLUTE", filepath.ToSlash(outside))

	_, _, err := WriteDiff(context.Background(), diff, Project{CWD: root})
	assertSandboxDenial(t, err)
	assertPathMissing(t, outside)
}

func TestWriteDiffTraversalRejectedWithSandboxDenial(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Clean(filepath.Join(root, "..", "outside.txt"))

	diff := `--- a/file.txt
+++ b/../outside.txt
@@ -0,0 +1 @@
+owned
`

	_, _, err := WriteDiff(context.Background(), diff, Project{CWD: root})
	assertSandboxDenial(t, err)
	assertPathMissing(t, outside)
}

func TestWriteDiffSymlinkEscapeRejectedWithSandboxDenial(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	link := filepath.Join(root, "linked")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unavailable on this platform: %v", err)
	}
	outsideTarget := filepath.Join(outside, "owned.txt")

	diff := `--- a/linked/owned.txt
+++ b/linked/owned.txt
@@ -0,0 +1 @@
+owned
`

	_, _, err := WriteDiff(context.Background(), diff, Project{CWD: root})
	assertSandboxDenial(t, err)
	assertPathMissing(t, outsideTarget)
}

func TestWriteDiffEscapeAbortsWithoutPartialWrite(t *testing.T) {
	root := t.TempDir()
	writeProjectFile(t, root, "safe.txt", "safe\n")

	diff := `--- a/safe.txt
+++ b/safe.txt
@@ -1 +1 @@
-safe
+changed
--- a/escape.txt
+++ b/../escape.txt
@@ -0,0 +1 @@
+owned
`

	_, _, err := WriteDiff(context.Background(), diff, Project{CWD: root})
	assertSandboxDenial(t, err)
	assertProjectFile(t, root, "safe.txt", "safe\n")
	assertPathMissing(t, filepath.Clean(filepath.Join(root, "..", "escape.txt")))
}

func assertSandboxDenial(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("error = nil, want SandboxDenial")
	}
	var cliErr *types.CLIError
	if !errors.As(err, &cliErr) {
		t.Fatalf("error type = %T, want *types.CLIError", err)
	}
	if cliErr.Code != types.CLIErrorCodeSandboxDenial {
		t.Fatalf("CLIError code = %s, want %s", cliErr.Code, types.CLIErrorCodeSandboxDenial)
	}
	if cliErr.Retryable {
		t.Fatal("Retryable = true, want false")
	}
	if !strings.HasPrefix(cliErr.Message, "path escapes worktree root: ") {
		t.Fatalf("message = %q, want path escape message", cliErr.Message)
	}
}

func assertPathMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("%s exists after rejected diff: stat err=%v", path, err)
	}
}
