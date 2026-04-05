package orchestrator_test

import (
	"testing"

	"github.com/thebtf/aimux/pkg/orchestrator"
)

const sampleDiff = `diff --git a/main.go b/main.go
index abc1234..def5678 100644
--- a/main.go
+++ b/main.go
@@ -1,5 +1,6 @@
 package main

 import "fmt"
+import "os"

 func main() {
@@ -10,3 +11,5 @@ func main() {
 	fmt.Println("hello")
+	fmt.Println("world")
+	os.Exit(0)
 }
diff --git a/util.go b/util.go
index 1111111..2222222 100644
--- a/util.go
+++ b/util.go
@@ -1,3 +1,4 @@
 package main

+// Helper function
 func helper() {}
`

func TestParseUnifiedDiff(t *testing.T) {
	files := orchestrator.ParseUnifiedDiff(sampleDiff)

	if len(files) != 2 {
		t.Fatalf("expected 2 files, got %d", len(files))
	}

	// First file: main.go with 2 hunks
	if files[0].Path != "main.go" {
		t.Errorf("file 0 path = %q, want main.go", files[0].Path)
	}
	if len(files[0].Hunks) != 2 {
		t.Errorf("file 0 hunks = %d, want 2", len(files[0].Hunks))
	}

	hunk0 := files[0].Hunks[0]
	if hunk0.AddLines != 1 {
		t.Errorf("hunk 0 add_lines = %d, want 1", hunk0.AddLines)
	}

	hunk1 := files[0].Hunks[1]
	if hunk1.AddLines != 2 {
		t.Errorf("hunk 1 add_lines = %d, want 2", hunk1.AddLines)
	}

	// Second file: util.go with 1 hunk
	if files[1].Path != "util.go" {
		t.Errorf("file 1 path = %q, want util.go", files[1].Path)
	}
	if len(files[1].Hunks) != 1 {
		t.Errorf("file 1 hunks = %d, want 1", len(files[1].Hunks))
	}
}

func TestAllHunks(t *testing.T) {
	files := orchestrator.ParseUnifiedDiff(sampleDiff)
	hunks := orchestrator.AllHunks(files)

	if len(hunks) != 3 {
		t.Errorf("AllHunks = %d, want 3", len(hunks))
	}
}

func TestParseUnifiedDiff_Empty(t *testing.T) {
	files := orchestrator.ParseUnifiedDiff("")
	if len(files) != 0 {
		t.Errorf("expected 0 files for empty input, got %d", len(files))
	}
}

func TestParseUnifiedDiff_NoDiff(t *testing.T) {
	files := orchestrator.ParseUnifiedDiff("just some text without any diff content")
	if len(files) != 0 {
		t.Errorf("expected 0 files for non-diff input, got %d", len(files))
	}
}

func TestReassembleDiff(t *testing.T) {
	files := orchestrator.ParseUnifiedDiff(sampleDiff)
	hunks := orchestrator.AllHunks(files)

	// Approve first and third hunk only
	approved := map[int]string{
		hunks[0].Index: hunks[0].Content,
		hunks[2].Index: hunks[2].Content,
	}

	result := orchestrator.ReassembleDiff(files, approved)

	if result == "" {
		t.Error("reassembled diff should not be empty")
	}

	// Should contain both files since hunks from each were approved
	if !contains(result, "main.go") {
		t.Error("should contain main.go")
	}
	if !contains(result, "util.go") {
		t.Error("should contain util.go")
	}
}

func contains(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 && (s == substr || len(s) > len(substr) && containsStr(s, substr))
}

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
