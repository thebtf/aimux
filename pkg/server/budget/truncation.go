package budget

import (
	"fmt"
	"strings"
)

// TruncationMeta carries truncation marker fields for brief responses.
type TruncationMeta struct {
	Truncated     bool   `json:"truncated,omitempty"`
	Hint          string `json:"hint,omitempty"`
	ContentLength int    `json:"content_length,omitempty"`
}

// BuildTruncationMeta constructs truncation metadata.
// No omissions + contentLength=0 -> TruncationMeta{} (Truncated=false, EC-1 compliant).
// Content omitted: Hint = "content omitted (N bytes). <hintTemplate>"
// Fields omitted only: Hint = "fields omitted: [f1 f2]. <hintTemplate>"
// Both: Hint = "content omitted (N bytes); fields omitted: [f1 f2]. <hintTemplate>"
func BuildTruncationMeta(omittedFields []string, contentLength int, hintTemplate string) TruncationMeta {
	hasFields := len(omittedFields) > 0
	hasContent := contentLength > 0

	if !hasFields && !hasContent {
		return TruncationMeta{}
	}

	meta := TruncationMeta{
		Truncated:     true,
		ContentLength: contentLength,
	}

	var parts []string
	if hasContent {
		parts = append(parts, fmt.Sprintf("content omitted (%d bytes)", contentLength))
	}
	if hasFields {
		parts = append(parts, fmt.Sprintf("fields omitted: [%s]", strings.Join(omittedFields, " ")))
	}
	meta.Hint = strings.Join(parts, "; ")

	if hintTemplate != "" {
		meta.Hint = fmt.Sprintf("%s. %s", meta.Hint, hintTemplate)
	}

	return meta
}
