package code

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteDiffCleanApplies(t *testing.T) {
	root := t.TempDir()
	writeProjectFile(t, root, "hello.txt", "one\ntwo\nthree\n")

	diff := `diff --git a/hello.txt b/hello.txt
index 1111111..2222222 100644
--- a/hello.txt
+++ b/hello.txt
@@ -1,3 +1,3 @@
 one
-two
+TWO
 three
`
	files, hunks, err := WriteDiff(context.Background(), diff, Project{CWD: root})
	if err != nil {
		t.Fatalf("WriteDiff returned error: %v", err)
	}
	if files != 1 || hunks != 1 {
		t.Fatalf("WriteDiff counts = (%d,%d), want (1,1)", files, hunks)
	}
	assertProjectFile(t, root, "hello.txt", "one\nTWO\nthree\n")
}

func TestWriteDiffInvalidSecondPatchRollsBackFirst(t *testing.T) {
	root := t.TempDir()
	writeProjectFile(t, root, "a.txt", "alpha\n")
	writeProjectFile(t, root, "b.txt", "bravo\n")

	diff := `--- a/a.txt
+++ b/a.txt
@@ -1 +1 @@
-alpha
+ALPHA
--- a/b.txt
+++ b/b.txt
@@ -1 +1 @@
?invalid
`
	_, _, err := WriteDiff(context.Background(), diff, Project{CWD: root})
	if err == nil {
		t.Fatal("WriteDiff returned nil, want invalid hunk error")
	}
	assertProjectFile(t, root, "a.txt", "alpha\n")
	assertProjectFile(t, root, "b.txt", "bravo\n")
}

func TestWriteDiffAbsolutePathRejected(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.txt")

	diff := strings.ReplaceAll(`--- a/file.txt
+++ ABSOLUTE
@@ -0,0 +1 @@
+owned
`, "ABSOLUTE", filepath.ToSlash(outside))

	_, _, err := WriteDiff(context.Background(), diff, Project{CWD: root})
	if err == nil {
		t.Fatal("WriteDiff returned nil, want absolute path rejection")
	}
	if _, statErr := os.Stat(outside); !os.IsNotExist(statErr) {
		t.Fatalf("outside path exists after rejected diff: stat err=%v", statErr)
	}
}

func TestWriteDiffPreservesTopLevelADirectory(t *testing.T) {
	root := t.TempDir()
	writeProjectFile(t, root, "a/foo.txt", "old\n")
	writeProjectFile(t, root, "foo.txt", "old\n")

	diff := `--- a/a/foo.txt
+++ b/a/foo.txt
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
	assertProjectFile(t, root, "a/foo.txt", "new\n")
	assertProjectFile(t, root, "foo.txt", "old\n")
}

func TestWriteDiffUnsupportedFileModeRejected(t *testing.T) {
	root := t.TempDir()

	diff := `diff --git a/link b/link
new file mode 120000
index 0000000..1111111
--- /dev/null
+++ b/link
@@ -0,0 +1 @@
+target
`
	_, _, err := WriteDiff(context.Background(), diff, Project{CWD: root})
	if err == nil {
		t.Fatal("WriteDiff returned nil, want unsupported file mode error")
	}
	if !strings.Contains(err.Error(), "unsupported file mode") {
		t.Fatalf("WriteDiff error = %v, want unsupported file mode", err)
	}
	assertPathMissing(t, filepath.Join(root, "link"))
}

func writeProjectFile(t *testing.T, root string, rel string, content string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func assertProjectFile(t *testing.T, root string, rel string, want string) {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	if string(content) != want {
		t.Fatalf("%s content = %q, want %q", rel, string(content), want)
	}
}
