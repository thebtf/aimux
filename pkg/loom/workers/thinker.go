package workers

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/thebtf/aimux/pkg/loom"
	"github.com/thebtf/aimux/pkg/think"
)

// ThinkerWorker adapts think patterns to the loom.Worker interface.
type ThinkerWorker struct{}

// NewThinkerWorker creates a ThinkerWorker.
func NewThinkerWorker() *ThinkerWorker { return &ThinkerWorker{} }

func (w *ThinkerWorker) Type() loom.WorkerType { return loom.WorkerTypeThinker }

func (w *ThinkerWorker) Execute(_ context.Context, task *loom.Task) (*loom.WorkerResult, error) {
	start := time.Now()

	// Extract pattern name from metadata.
	patternName, _ := task.Metadata["pattern"].(string)
	if patternName == "" {
		return nil, fmt.Errorf("thinker worker: missing 'pattern' in metadata")
	}

	handler := think.GetPattern(patternName)
	if handler == nil {
		return nil, fmt.Errorf("thinker worker: unknown pattern %q", patternName)
	}

	// Build input map from metadata (everything except "pattern" key).
	input := make(map[string]any)
	for k, v := range task.Metadata {
		if k != "pattern" {
			input[k] = v
		}
	}
	// Add prompt as "issue"/"topic" if not already in metadata.
	if task.Prompt != "" {
		if _, exists := input["issue"]; !exists {
			if _, exists := input["topic"]; !exists {
				input["issue"] = task.Prompt
			}
		}
	}

	validInput, err := handler.Validate(input)
	if err != nil {
		return nil, fmt.Errorf("thinker worker: validate: %w", err)
	}

	sessionID := ""
	if sid, ok := task.Metadata["session_id"].(string); ok {
		sessionID = sid
	}

	result, err := handler.Handle(validInput, sessionID)
	if err != nil {
		return nil, fmt.Errorf("thinker worker: handle: %w", err)
	}

	duration := time.Since(start).Milliseconds()

	// Use Summary if present, otherwise marshal Data to JSON.
	content := result.Summary
	if content == "" {
		b, _ := json.Marshal(result.Data)
		content = string(b)
	}

	return &loom.WorkerResult{
		Content:    content,
		Metadata:   map[string]any{"pattern": result.Pattern, "status": result.Status},
		DurationMS: duration,
	}, nil
}
