//go:build !windows

package code

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestWriteDiffNewExecutableFileMode(t *testing.T) {
	root := t.TempDir()
	oldUmask := syscall.Umask(0o077)
	t.Cleanup(func() {
		syscall.Umask(oldUmask)
	})

	diff := `diff --git a/run.sh b/run.sh
new file mode 100755
index 0000000..1111111
--- /dev/null
+++ b/run.sh
@@ -0,0 +1,2 @@
+#!/bin/sh
+echo ok
`
	files, hunks, err := WriteDiff(context.Background(), diff, Project{CWD: root})
	if err != nil {
		t.Fatalf("WriteDiff returned error: %v", err)
	}
	if files != 1 || hunks != 1 {
		t.Fatalf("WriteDiff counts = (%d,%d), want (1,1)", files, hunks)
	}
	info, err := os.Stat(filepath.Join(root, "run.sh"))
	if err != nil {
		t.Fatalf("stat run.sh: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o755 {
		t.Fatalf("run.sh mode = %#o, want 0755", got)
	}
	assertProjectFile(t, root, "run.sh", "#!/bin/sh\necho ok\n")
}

func TestWriteDiffExistingFileModeChange(t *testing.T) {
	root := t.TempDir()
	writeProjectFile(t, root, "run.sh", "echo old\n")

	diff := `diff --git a/run.sh b/run.sh
old mode 100644
new mode 100755
index 1111111..2222222
--- a/run.sh
+++ b/run.sh
@@ -1 +1 @@
-echo old
+echo new
`
	files, hunks, err := WriteDiff(context.Background(), diff, Project{CWD: root})
	if err != nil {
		t.Fatalf("WriteDiff returned error: %v", err)
	}
	if files != 1 || hunks != 1 {
		t.Fatalf("WriteDiff counts = (%d,%d), want (1,1)", files, hunks)
	}
	info, err := os.Stat(filepath.Join(root, "run.sh"))
	if err != nil {
		t.Fatalf("stat run.sh: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o755 {
		t.Fatalf("run.sh mode = %#o, want 0755", got)
	}
	assertProjectFile(t, root, "run.sh", "echo new\n")
}

func TestWriteDiffRollbackRestoresExecutableMode(t *testing.T) {
	root := t.TempDir()
	writeProjectFile(t, root, "run.sh", "echo old\n")
	runPath := filepath.Join(root, "run.sh")
	if err := os.Chmod(runPath, 0o755); err != nil {
		t.Fatalf("chmod run.sh: %v", err)
	}

	diff := `diff --git a/run.sh b/run.sh
old mode 100755
new mode 100644
index 1111111..2222222
--- a/run.sh
+++ b/run.sh
@@ -1 +1 @@
-echo old
+echo new
diff --git a/blocker b/blocker
new file mode 100644
index 0000000..1111111
--- /dev/null
+++ b/blocker
@@ -0,0 +1 @@
+blocker
diff --git a/blocker/child b/blocker/child
new file mode 100644
index 0000000..1111111
--- /dev/null
+++ b/blocker/child
@@ -0,0 +1 @@
+child
`
	_, _, err := WriteDiff(context.Background(), diff, Project{CWD: root})
	if err == nil {
		t.Fatal("WriteDiff returned nil, want rollback-triggering write error")
	}
	info, statErr := os.Stat(runPath)
	if statErr != nil {
		t.Fatalf("stat run.sh: %v", statErr)
	}
	if got := info.Mode().Perm(); got != 0o755 {
		t.Fatalf("run.sh mode after rollback = %#o, want 0755", got)
	}
	assertProjectFile(t, root, "run.sh", "echo old\n")
	assertPathMissing(t, filepath.Join(root, "blocker"))
}

func TestWriteDiffModeOnlyPatchDoesNotCarryMode(t *testing.T) {
	root := t.TempDir()
	writeProjectFile(t, root, "other.txt", "old\n")
	otherPath := filepath.Join(root, "other.txt")
	if err := os.Chmod(otherPath, 0o644); err != nil {
		t.Fatalf("chmod other.txt: %v", err)
	}

	diff := `diff --git a/chmod-only.sh b/chmod-only.sh
old mode 100644
new mode 100755
diff --git a/other.txt b/other.txt
index 1111111..2222222
--- a/other.txt
+++ b/other.txt
@@ -1 +1 @@
-old
+new
`
	files, hunks, err := WriteDiff(context.Background(), diff, Project{CWD: root})
	if err != nil {
		t.Fatalf("WriteDiff returned error: %v", err)
	}
	if files != 1 || hunks != 1 {
		t.Fatalf("WriteDiff counts = (%d,%d), want (1,1)", files, hunks)
	}
	info, err := os.Stat(otherPath)
	if err != nil {
		t.Fatalf("stat other.txt: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o644 {
		t.Fatalf("other.txt mode = %#o, want 0644", got)
	}
	assertProjectFile(t, root, "other.txt", "new\n")
}
