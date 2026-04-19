package util

import (
	"strings"
	"testing"
)

func TestTruncateUTF8_Empty(t *testing.T) {
	if got := TruncateUTF8("", 100); got != "" {
		t.Errorf("empty string: got %q, want %q", got, "")
	}
}

func TestTruncateUTF8_ZeroMax(t *testing.T) {
	if got := TruncateUTF8("hello", 0); got != "" {
		t.Errorf("zero maxBytes: got %q, want %q", got, "")
	}
}

func TestTruncateUTF8_ShortASCII_UnderLimit(t *testing.T) {
	s := "hello"
	if got := TruncateUTF8(s, 100); got != s {
		t.Errorf("short ASCII: got %q, want %q", got, s)
	}
}

func TestTruncateUTF8_ExactLimit(t *testing.T) {
	s := "hello"
	if got := TruncateUTF8(s, 5); got != s {
		t.Errorf("exact limit: got %q, want %q", got, s)
	}
}

func TestTruncateUTF8_LongASCII(t *testing.T) {
	s := strings.Repeat("a", 150)
	got := TruncateUTF8(s, 100)
	if len(got) != 100 {
		t.Errorf("long ASCII: len=%d, want 100", len(got))
	}
	if got != s[:100] {
		t.Errorf("long ASCII: got %q, want %q", got, s[:100])
	}
}

func TestTruncateUTF8_Russian_NoBoundaryBreak(t *testing.T) {
	// Each Russian letter is 2 bytes in UTF-8. With maxBytes=100,
	// we should get exactly 50 letters (100 bytes), never a split codepoint.
	s := strings.Repeat("а", 60) // 60 × 2 = 120 bytes
	got := TruncateUTF8(s, 100)
	if len(got) != 100 {
		t.Errorf("Russian: len=%d, want 100", len(got))
	}
	// Count runes to verify no codepoint was split (50 full letters expected).
	runeCount := 0
	for range got {
		runeCount++
	}
	if runeCount != 50 {
		t.Errorf("Russian: rune count=%d, want 50", runeCount)
	}
}

func TestTruncateUTF8_Emoji_NoBoundaryBreak(t *testing.T) {
	// Each emoji (e.g. 🚀) is 4 bytes in UTF-8.
	// With maxBytes=10, we should get 2 emojis (8 bytes), not 2.5.
	s := strings.Repeat("🚀", 5) // 5 × 4 = 20 bytes
	got := TruncateUTF8(s, 10)
	if len(got) != 8 {
		t.Errorf("emoji maxBytes=10: len=%d, want 8 (2 full emojis)", len(got))
	}
	runeCount := 0
	for range got {
		runeCount++
	}
	if runeCount != 2 {
		t.Errorf("emoji: rune count=%d, want 2", runeCount)
	}
}

func TestTruncateUTF8_OddBoundary_RussianAt100(t *testing.T) {
	// "обработано: 42" — if it's slightly over 100 bytes, truncate to last full codepoint.
	// Simulate: 49 Russian letters (98 bytes) + "x" (1 byte) = 99 bytes → under limit
	// 50 Russian letters (100 bytes) + "x" (1 byte) = 101 bytes → truncate to 100 bytes
	s := strings.Repeat("о", 50) + "x" // 100 + 1 = 101 bytes
	got := TruncateUTF8(s, 100)
	if len(got) != 100 {
		t.Errorf("odd boundary: len=%d, want 100", len(got))
	}
	// Verify the last character is 'о' (2-byte), not a partial byte.
	runeCount := 0
	for range got {
		runeCount++
	}
	if runeCount != 50 {
		t.Errorf("odd boundary: rune count=%d, want 50", runeCount)
	}
}

// Swap-body test: verifies that returning "" would fail tests (mutation guard).
func TestTruncateUTF8_SwapBodyGuard(t *testing.T) {
	// If TruncateUTF8 returned "" for all inputs, this test would fail.
	if got := TruncateUTF8("a", 1); got == "" {
		t.Error("stub guard: TruncateUTF8 returned empty for non-empty input within budget")
	}
}
