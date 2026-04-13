// Package prompt implements composable prompt templates loaded from prompts.d/.
// Constitution P14: Prompts = reusable fragments with includes composition.
package prompt

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Template represents a loaded prompt template.
type Template struct {
	Name     string
	Content  string
	Includes []string          // names of included templates
	Vars     map[string]string // template variables
}

// Engine loads and resolves prompt templates from prompts.d/ directories.
// Supports includes, output styles, and per-project overrides.
type Engine struct {
	templates map[string]*Template
	dirs      []string // search directories in priority order
	mu        sync.RWMutex
}

// NewEngine creates a prompt engine with the given template directories.
// Later directories take priority (project overrides built-in).
func NewEngine(dirs ...string) *Engine {
	return &Engine{
		templates: make(map[string]*Template),
		dirs:      dirs,
	}
}

// Load scans all directories (recursively) for .md and .txt template files.
// Subdirectory files are registered by filename only (no path prefix),
// so roles/coding.md becomes template "coding".
func (e *Engine) Load() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	for _, dir := range e.dirs {
		err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return nil // skip unreadable entries
			}
			if d.IsDir() {
				return nil
			}
			name := d.Name()
			if !strings.HasSuffix(name, ".md") && !strings.HasSuffix(name, ".txt") {
				return nil
			}

			content, err := os.ReadFile(path)
			if err != nil {
				return nil
			}

			tmplName := strings.TrimSuffix(strings.TrimSuffix(name, ".md"), ".txt")
			if _, exists := e.templates[tmplName]; exists {
				log.Printf("[aimux:prompt] template name collision: %q overwritten by %s", tmplName, path)
			}
			e.templates[tmplName] = &Template{
				Name:    tmplName,
				Content: string(content),
				Vars:    make(map[string]string),
			}
			return nil
		})
		if err != nil {
			continue // directory doesn't exist — skip silently
		}
	}

	return nil
}

// Get returns a template by name.
func (e *Engine) Get(name string) (*Template, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	tmpl, ok := e.templates[name]
	if !ok {
		return nil, fmt.Errorf("template %q not found", name)
	}
	return tmpl, nil
}

// Resolve processes a template by expanding includes and variables.
// Max include depth = 5 to prevent circular references.
func (e *Engine) Resolve(name string, vars map[string]string) (string, error) {
	return e.resolveRecursive(name, vars, 0)
}

func (e *Engine) resolveRecursive(name string, vars map[string]string, depth int) (string, error) {
	if depth > 5 {
		return "", fmt.Errorf("template include depth exceeded (max 5) — possible circular reference")
	}

	e.mu.RLock()
	tmpl, ok := e.templates[name]
	e.mu.RUnlock()

	if !ok {
		return "", fmt.Errorf("template %q not found", name)
	}

	content := tmpl.Content

	// Expand includes: {{include "name"}}
	for {
		start := strings.Index(content, "{{include \"")
		if start < 0 {
			break
		}
		end := strings.Index(content[start+11:], "\"}}")
		if end < 0 {
			break
		}

		includeName := content[start+11 : start+11+end]
		includeContent, err := e.resolveRecursive(includeName, vars, depth+1)
		if err != nil {
			return "", fmt.Errorf("include %q: %w", includeName, err)
		}

		content = content[:start] + includeContent + content[start+11+end+3:]
	}

	// Expand variables: {{var}}
	for k, v := range vars {
		content = strings.ReplaceAll(content, "{{"+k+"}}", v)
	}

	return content, nil
}

// List returns all loaded template names.
func (e *Engine) List() []string {
	e.mu.RLock()
	defer e.mu.RUnlock()

	names := make([]string, 0, len(e.templates))
	for name := range e.templates {
		names = append(names, name)
	}
	return names
}

// Register manually adds a template (for built-in templates).
func (e *Engine) Register(name, content string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.templates[name] = &Template{
		Name:    name,
		Content: content,
		Vars:    make(map[string]string),
	}
}
