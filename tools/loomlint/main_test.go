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

	if err := Lint(dir, defaultAllowPrefixes); err != nil {
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

	err := Lint(dir, defaultAllowPrefixes)
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

	// Clean file at root level.
	writeGoFile(t, root, "ok.go", `package ok

import "fmt"

var _ = fmt.Sprintf
`)

	// Forbidden import in nested subdir.
	writeGoFile(t, sub, "nested.go", `package pkg

import "github.com/thebtf/aimux/pkg/executor"

var _ = "nested violation"
`)

	err := Lint(root, defaultAllowPrefixes)
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
		// Sub-package of an allowed prefix must pass.
		{"github.com/thebtf/aimux/pkg/loom/workers", true},
		{"github.com/thebtf/aimux/pkg/loom/deps", true},
		// Different package under same parent must fail.
		{"github.com/thebtf/aimux/pkg/types", false},
		{"github.com/thebtf/aimux/pkg/executor", false},
		// Otel metric is allowed; its sub-packages too.
		{"go.opentelemetry.io/otel/metric", true},
		{"go.opentelemetry.io/otel/metric/noop", true},
		// Otel SDK (exporter) is not allowed.
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

	// With default prefixes this must fail.
	if err := Lint(dir, defaultAllowPrefixes); err == nil {
		t.Fatal("expected failure with default allow-list")
	}

	// Adding pkg/types as an allowed prefix makes it pass.
	extended := append(defaultAllowPrefixes, "github.com/thebtf/aimux/pkg/types")
	if err := Lint(dir, extended); err != nil {
		t.Fatalf("expected nil with extended allow-list, got: %v", err)
	}
}
