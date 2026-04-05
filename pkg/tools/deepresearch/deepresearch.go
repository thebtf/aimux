// Package deepresearch implements the Google GenAI Deep Research integration.
// Uses Gemini Interactions API for long-form research with file attachments.
package deepresearch

// DeepResearch handles Google GenAI Deep Research API calls.
// This is an API-based tool (not CLI-based) — uses HTTP transport directly.
type DeepResearch struct {
	model          string
	timeoutSeconds int
}

// New creates a DeepResearch handler.
func New(model string, timeoutSeconds int) *DeepResearch {
	if model == "" {
		model = "deep-research-pro-preview-12-2025"
	}
	if timeoutSeconds == 0 {
		timeoutSeconds = 1800 // 30 minutes
	}
	return &DeepResearch{
		model:          model,
		timeoutSeconds: timeoutSeconds,
	}
}

// Model returns the configured model name.
func (d *DeepResearch) Model() string { return d.model }

// TimeoutSeconds returns the configured timeout.
func (d *DeepResearch) TimeoutSeconds() int { return d.timeoutSeconds }

// Execute runs a deep research query.
// Full implementation requires Google GenAI SDK integration (Phase 8).
// For now, returns a placeholder indicating the tool is registered.
func (d *DeepResearch) Execute(topic string, outputFormat string, files []string) (string, error) {
	return "DeepResearch tool registered. Google GenAI API integration pending.", nil
}
