// Package util provides shared utility functions used across aimux packages.
package util

import "unicode/utf8"

// TruncateUTF8 returns s truncated to at most maxBytes UTF-8 bytes without
// splitting a multi-byte codepoint. If len(s) <= maxBytes, s is returned as-is
// (O(1)). Otherwise the string is scanned to find the nearest valid UTF-8
// boundary at or before maxBytes.
//
// Guarantees:
//   - len(result) <= maxBytes
//   - result is always valid UTF-8 (no mojibake)
//   - ASCII strings are handled identically to multi-byte strings
func TruncateUTF8(s string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(s) <= maxBytes {
		return s
	}
	// Walk forward until we would exceed maxBytes or exhaust the string.
	// utf8.DecodeRuneInString is O(1) per codepoint and safe for invalid UTF-8.
	boundary := 0
	for boundary < maxBytes {
		_, size := utf8.DecodeRuneInString(s[boundary:])
		if boundary+size > maxBytes {
			break
		}
		boundary += size
	}
	return s[:boundary]
}
