package main

import (
	"strings"
	"testing"
)

// TestIntegrationForbiddenImportDetected creates a temp directory containing a
// Go file with a deliberately forbidden import, calls Lint programmatically,
// and asserts the violation is detected. This is the primary T004 requirement:
// the boundary enforcement is not a stub — replacing the AST walk with a no-op
// must cause this test to fail.
func TestIntegrationForbiddenImportDetected(t *testing.T) {
	dir := t.TempDir()

	// Write a file with a forbidden import — simulates a developer accidentally
	// adding an aimux internal dependency into loom core.
	writeGoFile(t, dir, "forbidden.go", `package loom

import "github.com/thebtf/aimux/pkg/types"

// deliberate boundary violation for integration test
var _ = "violation"
`)

	err := Lint(dir, defaultOpts())
	if err == nil {
		t.Fatal("Lint returned nil for a file with a forbidden import — boundary enforcement is broken")
	}
	if !strings.Contains(err.Error(), "pkg/types") {
		t.Errorf("error message does not mention forbidden package: %v", err)
	}
}

// TestIntegrationCleanDirectoryPasses verifies that Lint returns nil for a
// directory that only uses stdlib and allowed imports.
func TestIntegrationCleanDirectoryPasses(t *testing.T) {
	dir := t.TempDir()

	writeGoFile(t, dir, "clean.go", `package loom

import (
	"context"
	"sync"
	"github.com/google/uuid"
)

var _ = context.Background
var _ = sync.Mutex{}
var _ = uuid.New
`)

	if err := Lint(dir, defaultOpts()); err != nil {
		t.Fatalf("Lint returned error for clean directory: %v", err)
	}
}

// TestIntegrationAllowedSubpackagePass verifies that loom sub-packages
// (e.g. pkg/loom/workers, pkg/loom/deps) are treated as allowed because
// they share the github.com/thebtf/aimux/pkg/loom prefix.
func TestIntegrationAllowedSubpackagePass(t *testing.T) {
	dir := t.TempDir()

	// These imports are allowed because they start with the loom prefix.
	writeGoFile(t, dir, "subpkg.go", `package loom

import (
	"github.com/thebtf/aimux/pkg/loom/workers"
	"github.com/thebtf/aimux/pkg/loom/deps"
)

var _ = workers.NewCLIWorker
var _ = deps.NoopLogger
`)

	if err := Lint(dir, defaultOpts()); err != nil {
		t.Fatalf("Lint rejected allowed loom sub-package: %v", err)
	}
}

// TestIntegrationMultipleViolationsAllReported verifies that when multiple
// files in a directory each contain violations, all violations appear in the
// error message — not just the first one.
func TestIntegrationMultipleViolationsAllReported(t *testing.T) {
	dir := t.TempDir()

	writeGoFile(t, dir, "a.go", `package loom

import "github.com/thebtf/aimux/pkg/types"

var _ = "a"
`)
	writeGoFile(t, dir, "b.go", `package loom

import "github.com/thebtf/aimux/pkg/executor"

var _ = "b"
`)

	err := Lint(dir, defaultOpts())
	if err == nil {
		t.Fatal("expected non-nil error for multiple violations, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "pkg/types") {
		t.Errorf("missing pkg/types violation in error: %s", msg)
	}
	if !strings.Contains(msg, "pkg/executor") {
		t.Errorf("missing pkg/executor violation in error: %s", msg)
	}
}
