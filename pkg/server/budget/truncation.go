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

	if hasContent && hasFields {
		meta.Hint = fmt.Sprintf("content omitted (%d bytes); fields omitted: [%s]", contentLength, strings.Join(omittedFields, " "))
	}
	if hasContent && !hasFields {
		meta.Hint = fmt.Sprintf("content omitted (%d bytes)", contentLength)
	}
	if !hasContent && hasFields {
		meta.Hint = fmt.Sprintf("fields omitted: [%s]", strings.Join(omittedFields, " "))
	}

	if hintTemplate != "" {
		meta.Hint = fmt.Sprintf("%s. %s", meta.Hint, hintTemplate)
	}

	return meta
}
