package server

import (
	"os"
	"testing"
)

func TestValidateCWD_RejectsEmpty(t *testing.T) {
	if err := validateCWD(""); err == nil {
		t.Error("expected error for empty cwd")
	}
}

func TestValidateCWD_RejectsRelative(t *testing.T) {
	if err := validateCWD("relative/path"); err == nil {
		t.Error("expected error for relative path")
	}
}

func TestValidateCWD_RejectsMissing(t *testing.T) {
	if err := validateCWD("/this/path/does/not/exist/hopefully/xyzzy123"); err == nil {
		t.Error("expected error for non-existent path")
	}
}

func TestValidateCWD_RejectsNullByte(t *testing.T) {
	if err := validateCWD("/tmp/foo\x00bar"); err == nil {
		t.Error("expected error for null byte in cwd")
	}
}

func TestValidateCWD_RejectsNewline(t *testing.T) {
	if err := validateCWD("/tmp/foo\nbar"); err == nil {
		t.Error("expected error for newline in cwd")
	}
}

func TestValidateCWD_AcceptsValidAbsoluteDir(t *testing.T) {
	// Use the temp dir which always exists.
	tmpDir := os.TempDir()
	if err := validateCWD(tmpDir); err != nil {
		t.Errorf("expected no error for valid dir %q: %v", tmpDir, err)
	}
}
