package review

import (
	"strings"

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
	reason = strings.ToValidUTF8(reason, "\uFFFD")
	reason = strings.TrimSpace(redact.RedactSecrets(reason))
	if len(reason) <= PublicReasonMaxBytes {
		return reason
	}
	contentBudget := PublicReasonMaxBytes - len(PublicReasonTruncationMarker)
	if contentBudget <= 0 {
		return util.TruncateUTF8(PublicReasonTruncationMarker, PublicReasonMaxBytes)
	}
	return util.TruncateUTF8(reason, contentBudget) + PublicReasonTruncationMarker
}
