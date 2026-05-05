package build

import "testing"

func TestNormalizeVersionStripsKnownTagPrefixes(t *testing.T) {
	tests := map[string]string{
		"aimux/v5.6.1":       "5.6.1",
		"aimux/v5.6.1-dirty": "5.6.1-dirty",
		"v5.6.1":             "5.6.1",
		"5.6.1":              "5.6.1",
		"0.0.0-dev":          "0.0.0-dev",
	}

	for input, want := range tests {
		if got := normalizeVersion(input); got != want {
			t.Fatalf("normalizeVersion(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestStringUsesNormalizedVersion(t *testing.T) {
	oldVersion := Version
	oldCommit := Commit
	oldBuildDate := BuildDate
	t.Cleanup(func() {
		Version = oldVersion
		Commit = oldCommit
		BuildDate = oldBuildDate
	})

	Version = normalizeVersion("aimux/v5.6.1")
	Commit = "b1c2b59"
	BuildDate = "2026-05-05T16:58:22Z"

	got := String()
	want := "5.6.1 (commit b1c2b59, built 2026-05-05T16:58:22Z)"
	if got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
}
