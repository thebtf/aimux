package budget

import (
	"strings"
	"testing"
)

func TestBuildTruncationMeta(t *testing.T) {
	t.Run("no omissions", func(t *testing.T) {
		got := BuildTruncationMeta(nil, 0, "")
		if got.Truncated {
			t.Fatal("Truncated = true")
		}
		if got.Hint != "" {
			t.Fatalf("Hint = %q", got.Hint)
		}
		if got.ContentLength != 0 {
			t.Fatalf("ContentLength = %d", got.ContentLength)
		}
	})

	t.Run("content omitted", func(t *testing.T) {
		got := BuildTruncationMeta(nil, 140213, "Use include_content=true")
		if !got.Truncated {
			t.Fatal("Truncated = false")
		}
		if got.ContentLength != 140213 {
			t.Fatalf("ContentLength = %d", got.ContentLength)
		}
		if !strings.Contains(got.Hint, "140213 bytes") {
			t.Fatalf("Hint = %q", got.Hint)
		}
	})

	t.Run("only fields omitted", func(t *testing.T) {
		got := BuildTruncationMeta([]string{"status", "content_length"}, 0, "")
		if !got.Truncated {
			t.Fatal("Truncated = false")
		}
		if !strings.Contains(got.Hint, "fields omitted") {
			t.Fatalf("Hint = %q", got.Hint)
		}
	})

	t.Run("both content and fields omitted", func(t *testing.T) {
		got := BuildTruncationMeta([]string{"content", "hint"}, 100, "Use include_content=true")
		if !got.Truncated {
			t.Fatal("Truncated = false")
		}
		if !strings.Contains(got.Hint, "content omitted (100 bytes)") {
			t.Fatalf("Hint = %q", got.Hint)
		}
		if !strings.Contains(got.Hint, "fields omitted: [content hint]") {
			t.Fatalf("Hint = %q", got.Hint)
		}
	})

	t.Run("hint template appended", func(t *testing.T) {
		got := BuildTruncationMeta(nil, 10, "Increase limit to retrieve full content")
		if !strings.Contains(got.Hint, ". Increase limit to retrieve full content") {
			t.Fatalf("Hint = %q", got.Hint)
		}
	})
}
