package review

import (
	"strings"
	"unicode"

	"github.com/thebtf/aimux/pkg/executor/redact"
	"github.com/thebtf/aimux/pkg/util"
)

const (
	// PublicReasonMaxBytes is the strict UTF-8 byte ceiling for a review reason
	// exposed through decisions, task metadata, and default task resources.
	PublicReasonMaxBytes = 512

	// PublicReasonTruncationMarker makes bounded review reasons visibly partial.
	PublicReasonTruncationMarker = "...[truncated]"
)

// SanitizePublicReason redacts known secrets and applies the public review
// reason byte budget without splitting a UTF-8 code point.
func SanitizePublicReason(reason string) string {
	reason = strings.TrimSpace(sanitizePublicReviewText(reason))
	if len(reason) <= PublicReasonMaxBytes {
		return reason
	}
	contentBudget := PublicReasonMaxBytes - len(PublicReasonTruncationMarker)
	if contentBudget <= 0 {
		return util.TruncateUTF8(PublicReasonTruncationMarker, PublicReasonMaxBytes)
	}
	return util.TruncateUTF8(reason, contentBudget) + PublicReasonTruncationMarker
}

func sanitizePublicReviewText(text string) string {
	text = strings.ToValidUTF8(text, "\uFFFD")
	text = redact.RedactSecrets(text)
	normalized := strings.Map(func(r rune) rune {
		if !unicode.IsLetter(r) && !unicode.IsNumber(r) {
			return -1
		}
		return unicode.ToLower(r)
	}, text)
	for _, privateKind := range []string{
		"privatereasoning", "hiddenreasoning", "internalreasoning", "chainofthought",
		"privatethought", "hiddenthought", "internalthought",
	} {
		if strings.Contains(normalized, privateKind) {
			// The marker identifies the whole field as private; do not guess where its payload ends.
			return "[REDACTED:private-reasoning]"
		}
	}
	return text
}
