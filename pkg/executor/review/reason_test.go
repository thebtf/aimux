package review

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSanitizePublicReasonPreservesUTF8BoundaryAndStrictBudget(t *testing.T) {
	contentBudget := PublicReasonMaxBytes - len(PublicReasonTruncationMarker)
	raw := strings.Repeat("a", contentBudget-1) + "界" + strings.Repeat("b", 20)

	got := SanitizePublicReason(raw)
	if len(got) > PublicReasonMaxBytes {
		t.Fatalf("length = %d bytes, want <= %d", len(got), PublicReasonMaxBytes)
	}
	if !strings.HasSuffix(got, PublicReasonTruncationMarker) {
		t.Fatalf("reason = %q, want truncation marker", got)
	}
	if !utf8.ValidString(got) {
		t.Fatalf("reason is not valid UTF-8: %q", got)
	}
}

func TestSanitizePublicReasonRepairsInvalidUTF8(t *testing.T) {
	got := SanitizePublicReason("invalid \xff reason")
	if !utf8.ValidString(got) {
		t.Fatalf("reason is not valid UTF-8: %q", got)
	}
	if !strings.Contains(got, "\uFFFD") {
		t.Fatalf("reason = %q, want replacement rune", got)
	}
}
