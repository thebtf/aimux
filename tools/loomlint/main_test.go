package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeGoFile creates a minimal Go source file in dir with the given body.
func writeGoFile(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatalf("writeGoFile %s: %v", name, err)
	}
}

// defaultOpts returns a LintOptions with Phase 0 defaults for test reuse.
// Copies are returned so that any append in a test does not mutate the package-level globals.
func defaultOpts() LintOptions {
	return LintOptions{
		AllowPrefixes: append([]string(nil), defaultAllowPrefixes...),
		SkipDirs:      append([]string(nil), defaultSkipDirs...),
	}
}

// TestLintCleanDirectory verifies that a directory with only stdlib and
// allowed-prefix imports passes with nil error.
func TestLintCleanDirectory(t *testing.T) {
	dir := t.TempDir()
	writeGoFile(t, dir, "clean.go", `package clean

import (
	"context"
	"fmt"
	"github.com/google/uuid"
)

var _ = context.Background
var _ = fmt.Sprintf
var _ = uuid.New
`)

	if err := Lint(dir, defaultOpts()); err != nil {
		t.Fatalf("expected nil, got: %v", err)
	}
}

// TestLintSingleViolation verifies that a single forbidden import is caught
// and the error message includes the import path and file name.
func TestLintSingleViolation(t *testing.T) {
	dir := t.TempDir()
	writeGoFile(t, dir, "bad.go", `package bad

import "github.com/thebtf/aimux/pkg/types"

var _ = "unused"
`)

	err := Lint(dir, defaultOpts())
	if err == nil {
		t.Fatal("expected non-nil error for forbidden import, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "github.com/thebtf/aimux/pkg/types") {
		t.Errorf("error message missing import path: %s", msg)
	}
	if !strings.Contains(msg, "bad.go") {
		t.Errorf("error message missing file name: %s", msg)
	}
}

// TestLintNestedSubdirViolation verifies that violations inside a nested
// subdirectory are detected during the recursive walk.
func TestLintNestedSubdirViolation(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "sub", "pkg")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	writeGoFile(t, root, "ok.go", `package ok

import "fmt"

var _ = fmt.Sprintf
`)

	writeGoFile(t, sub, "nested.go", `package pkg

import "github.com/thebtf/aimux/pkg/executor"

var _ = "nested violation"
`)

	err := Lint(root, defaultOpts())
	if err == nil {
		t.Fatal("expected non-nil error for nested forbidden import, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "github.com/thebtf/aimux/pkg/executor") {
		t.Errorf("error message missing nested import path: %s", msg)
	}
	if !strings.Contains(msg, "nested.go") {
		t.Errorf("error message missing nested file name: %s", msg)
	}
}

// TestIsStdlib verifies the stdlib detection heuristic.
func TestIsStdlib(t *testing.T) {
	cases := []struct {
		path   string
		stdlib bool
	}{
		{"fmt", true},
		{"os/exec", true},
		{"encoding/json", true},
		{"runtime/debug", true},
		{"github.com/google/uuid", false},
		{"go.opentelemetry.io/otel/metric", false},
		{"golang.org/x/sys", false},
	}
	for _, c := range cases {
		got := isStdlib(c.path)
		if got != c.stdlib {
			t.Errorf("isStdlib(%q) = %v, want %v", c.path, got, c.stdlib)
		}
	}
}

// TestIsAllowed verifies prefix matching including loom sub-package rule.
func TestIsAllowed(t *testing.T) {
	allow := []string{
		"github.com/google/uuid",
		"go.opentelemetry.io/otel/metric",
		"github.com/thebtf/aimux/pkg/loom",
	}
	cases := []struct {
		path    string
		allowed bool
	}{
		{"fmt", true},
		{"github.com/google/uuid", true},
		{"github.com/thebtf/aimux/pkg/loom/workers", true},
		{"github.com/thebtf/aimux/pkg/loom/deps", true},
		{"github.com/thebtf/aimux/pkg/types", false},
		{"github.com/thebtf/aimux/pkg/executor", false},
		{"go.opentelemetry.io/otel/metric", true},
		{"go.opentelemetry.io/otel/metric/noop", true},
		{"go.opentelemetry.io/otel/sdk/metric", false},
	}
	for _, c := range cases {
		got := isAllowed(c.path, allow)
		if got != c.allowed {
			t.Errorf("isAllowed(%q) = %v, want %v", c.path, got, c.allowed)
		}
	}
}

// TestLintAllowFlagExtension verifies that passing a custom prefix list
// allows imports that would otherwise be forbidden.
func TestLintAllowFlagExtension(t *testing.T) {
	dir := t.TempDir()
	writeGoFile(t, dir, "extra.go", `package extra

import "github.com/thebtf/aimux/pkg/types"

var _ = "extra allowed"
`)

	if err := Lint(dir, defaultOpts()); err == nil {
		t.Fatal("expected failure with default allow-list")
	}

	extended := defaultOpts()
	extended.AllowPrefixes = append([]string{}, defaultAllowPrefixes...)
	extended.AllowPrefixes = append(extended.AllowPrefixes, "github.com/thebtf/aimux/pkg/types")
	if err := Lint(dir, extended); err != nil {
		t.Fatalf("expected nil with extended allow-list, got: %v", err)
	}
}

// TestLintSkipsTestFilesByDefault verifies that *_test.go files are NOT scanned
// unless opts.IncludeTests is true. Spec NFR-1 scopes boundary enforcement to
// production code only — test files legitimately import test drivers and
// fixtures that are out of scope for the core closure.
func TestLintSkipsTestFilesByDefault(t *testing.T) {
	dir := t.TempDir()

	writeGoFile(t, dir, "prod.go", `package only

import "fmt"

var _ = fmt.Sprintf
`)

	// This test file imports a forbidden driver. It must be ignored by default
	// because opts.IncludeTests defaults to false.
	writeGoFile(t, dir, "harness_test.go", `package only

import (
	"testing"
	_ "modernc.org/sqlite"
)

func TestHarness(t *testing.T) { _ = t }
`)

	opts := defaultOpts()
	if err := Lint(dir, opts); err != nil {
		t.Fatalf("expected nil with IncludeTests=false, got: %v", err)
	}

	// With IncludeTests=true, the same forbidden import is reported.
	opts.IncludeTests = true
	err := Lint(dir, opts)
	if err == nil {
		t.Fatal("expected violation with IncludeTests=true, got nil")
	}
	if !strings.Contains(err.Error(), "modernc.org/sqlite") {
		t.Errorf("expected violation to mention modernc.org/sqlite, got: %v", err)
	}
	if !strings.Contains(err.Error(), "harness_test.go") {
		t.Errorf("expected violation to mention harness_test.go, got: %v", err)
	}
}

// TestLintSkipsDirs verifies that directories matching opts.SkipDirs are
// excluded from the walk. This covers the workers/ subpackage case where
// aimux-specific adapter code legitimately imports aimux/pkg/*.
func TestLintSkipsDirs(t *testing.T) {
	root := t.TempDir()
	workers := filepath.Join(root, "workers")
	if err := os.MkdirAll(workers, 0o755); err != nil {
		t.Fatalf("MkdirAll workers: %v", err)
	}

	writeGoFile(t, root, "engine.go", `package loom

import "fmt"

var _ = fmt.Sprintf
`)

	writeGoFile(t, workers, "cli.go", `package workers

import "github.com/thebtf/aimux/pkg/types"

var _ = "adapter"
`)

	// With default SkipDirs = ["workers"], the workers/cli.go violation is skipped.
	if err := Lint(root, defaultOpts()); err != nil {
		t.Fatalf("expected nil with skip-dir workers, got: %v", err)
	}

	// With empty SkipDirs, the same violation is reported.
	opts := defaultOpts()
	opts.SkipDirs = nil
	err := Lint(root, opts)
	if err == nil {
		t.Fatal("expected violation with empty SkipDirs, got nil")
	}
	if !strings.Contains(err.Error(), "github.com/thebtf/aimux/pkg/types") {
		t.Errorf("expected violation to mention pkg/types, got: %v", err)
	}
}

// TestLintHonorsTargetAsSkipDirBase verifies that the walker does NOT treat
// the root target itself as a skippable directory even if its base name matches
// an entry in SkipDirs. Only sub-directories encountered during the walk should
// be excluded by name match.
func TestLintHonorsTargetAsSkipDirBase(t *testing.T) {
	root := t.TempDir()
	workers := filepath.Join(root, "workers")
	if err := os.MkdirAll(workers, 0o755); err != nil {
		t.Fatalf("MkdirAll workers: %v", err)
	}

	writeGoFile(t, workers, "cli.go", `package workers

import "github.com/thebtf/aimux/pkg/types"

var _ = "adapter"
`)

	// Point lint directly at the workers/ directory. Even though its base name
	// matches SkipDirs, the root target itself must be walked — otherwise the
	// user's explicit target would silently produce zero results.
	opts := defaultOpts()
	err := Lint(workers, opts)
	if err == nil {
		t.Fatal("expected violation when lint target base matches skip-dir list, got nil")
	}
	if !strings.Contains(err.Error(), "github.com/thebtf/aimux/pkg/types") {
		t.Errorf("expected violation on explicit target, got: %v", err)
	}
}
