// Package prompts renders embedded multi-pass review prompts.
package prompts

import (
	"bytes"
	"embed"
	"fmt"
	"text/template"
)

//go:embed structural.tmpl behavioural.tmpl adversarial.tmpl
var templateFS embed.FS

// CriterionView is the prompt-facing shape of one review criterion.
type CriterionView struct {
	Name        string
	Description string
}

// RenderData holds variables used by the embedded review templates.
type RenderData struct {
	Target   string
	Criteria []CriterionView
}

// RenderStructural renders the structural review prompt.
func RenderStructural(data RenderData) (string, error) {
	return render("structural.tmpl", data)
}

// RenderBehavioural renders the behavioural review prompt.
func RenderBehavioural(data RenderData) (string, error) {
	return render("behavioural.tmpl", data)
}

// RenderAdversarial renders the adversarial review prompt.
func RenderAdversarial(data RenderData) (string, error) {
	return render("adversarial.tmpl", data)
}

func render(name string, data RenderData) (string, error) {
	content, err := templateFS.ReadFile(name)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", name, err)
	}
	tmpl, err := template.New(name).Option("missingkey=error").Parse(string(content))
	if err != nil {
		return "", fmt.Errorf("parse %s: %w", name, err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("execute %s: %w", name, err)
	}
	if buf.Len() == 0 {
		return "", fmt.Errorf("render %s: empty prompt", name)
	}
	return buf.String(), nil
}
