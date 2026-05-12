// Package prompts renders embedded code-entry pair prompts.
package prompts

import (
	"bytes"
	"embed"
	"fmt"
	"text/template"
)

//go:embed driver.tmpl navigator.tmpl driver_solo.tmpl
var templateFS embed.FS

// CriterionView is the prompt-facing shape of one success criterion.
type CriterionView struct {
	Name        string
	Description string
	Weight      float64
}

// RenderData holds variables used by the embedded templates.
type RenderData struct {
	Prompt         string
	ProjectContext string
	CriteriaList   []CriterionView
	Diff           string
	Sandbox        string
}

// RenderDriver renders the readonly driver prompt.
func RenderDriver(data RenderData) (string, error) {
	return render("driver.tmpl", data)
}

// RenderDriverSolo renders the write-enabled solo driver prompt.
func RenderDriverSolo(data RenderData) (string, error) {
	return render("driver_solo.tmpl", data)
}

// RenderNavigator renders the navigator scoring prompt.
func RenderNavigator(data RenderData) (string, error) {
	return render("navigator.tmpl", data)
}

func render(name string, data RenderData) (string, error) {
	content, err := templateFS.ReadFile(name)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", name, err)
	}
	tmpl, err := template.New(name).Parse(string(content))
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
