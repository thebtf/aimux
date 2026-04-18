package budget

import "github.com/thebtf/aimux/pkg/guidance"

// AttachTruncation merges TruncationMeta fields into the Result sub-object of a
// ResponseEnvelope. Budget markers are placed on Result (plan Architecture L5),
// never on GuidanceFields (state, you_are_here, etc.).
//
// Rules:
//   - meta.Truncated=false → no fields added (EC-1: no marker when nothing omitted)
//   - envelope.Result nil → initialised to map[string]any{} before merge
//   - envelope.Result is map[string]any → fields merged directly
//   - envelope.Result is any other type → left unchanged (no double-wrapping)
func AttachTruncation(envelope *guidance.ResponseEnvelope, meta TruncationMeta) {
	if !meta.Truncated {
		return
	}
	if envelope == nil {
		return
	}

	switch r := envelope.Result.(type) {
	case nil:
		m := map[string]any{
			"truncated": meta.Truncated,
		}
		if meta.Hint != "" {
			m["hint"] = meta.Hint
		}
		if meta.ContentLength > 0 {
			m["content_length"] = meta.ContentLength
		}
		envelope.Result = m
	case map[string]any:
		r["truncated"] = meta.Truncated
		if meta.Hint != "" {
			r["hint"] = meta.Hint
		}
		if meta.ContentLength > 0 {
			r["content_length"] = meta.ContentLength
		}
	default:
		// Non-map result: left unchanged to avoid double-wrapping
	}
}
