package harness

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type packageInfo struct {
	GoFiles     []string `json:"GoFiles"`
	Imports     []string `json:"Imports"`
	TestImports []string `json:"TestImports"`
}

func TestPackageBoundary(t *testing.T) {
	info := loadPackageInfo(t)

	if len(info.GoFiles) == 0 {
		t.Fatal("harness package must contain at least one non-test Go file")
	}

	forbidden := []string{
		"github.com/thebtf/aimux/pkg/server",
		"github.com/sashabaranov/go-openai",
		"github.com/liushuangls/go-anthropic",
		"google.golang.org/genai",
	}
	for _, imp := range append(append([]string{}, info.Imports...), info.TestImports...) {
		for _, prefix := range forbidden {
			if imp == prefix || strings.HasPrefix(imp, prefix+"/") {
				t.Fatalf("harness package must not import %s; found %s", prefix, imp)
			}
		}
	}
}

func loadPackageInfo(t *testing.T) packageInfo {
	t.Helper()

	cmd := exec.Command("go", "list", "-json", ".")
	cmd.Dir = currentDir(t)
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			t.Fatalf("go list failed: %v\n%s", err, string(exitErr.Stderr))
		}
		t.Fatalf("go list failed: %v", err)
	}

	var info packageInfo
	if err := json.Unmarshal(out, &info); err != nil {
		t.Fatalf("decode go list output: %v", err)
	}
	return info
}

func currentDir(t *testing.T) string {
	t.Helper()

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	return filepath.Clean(wd)
}
