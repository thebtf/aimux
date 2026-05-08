package gate

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNormalizeCWDReturnsRealPath(t *testing.T) {
	target := t.TempDir()
	link := filepath.Join(t.TempDir(), "linked-project")
	if err := symlinkDirectory(target, link); err != nil {
		t.Skipf("directory symlink unavailable on this platform: %v", err)
	}

	got, err := normalizeCWD(link)
	if err != nil {
		t.Fatalf("normalizeCWD returned error: %v", err)
	}
	want, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatalf("EvalSymlinks target: %v", err)
	}
	if got != want {
		t.Fatalf("normalizeCWD = %q, want realpath %q", got, want)
	}
}

func symlinkDirectory(target string, link string) error {
	return os.Symlink(target, link)
}
