package dialogue

import "strings"

// sanitizeName removes control characters and limits length for safe
// interpolation into prompts and structured delimiters.
func sanitizeName(name string) string {
	clean := strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1 // drop control characters
		}
		return r
	}, name)
	if len(clean) > 64 {
		clean = clean[:64]
	}
	return clean
}

// sanitizeStance removes control characters and limits length for safe
// interpolation into system prompts.
func sanitizeStance(stance string) string {
	clean := strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1 // drop control characters
		}
		return r
	}, stance)
	if len(clean) > 128 {
		clean = clean[:128]
	}
	return clean
}
