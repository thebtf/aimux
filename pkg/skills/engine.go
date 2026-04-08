package skills

import (
	"bytes"
	"fmt"
	"io/fs"
	"log"
	"os"
	"strings"
	"sync"
	"text/template"

	"gopkg.in/yaml.v3"
)

// Engine loads skill templates from an embedded FS and an optional disk directory,
// renders them with SkillData, and exposes metadata for MCP prompt registration.
type Engine struct {
	skills    map[string]*SkillMeta
	templates *template.Template
	mu        sync.RWMutex
}

// NewEngine returns an empty, ready-to-use Engine.
// Call Load() to populate it before Render/Skills/Get.
func NewEngine() *Engine {
	return &Engine{
		skills: make(map[string]*SkillMeta),
	}
}

// parseFrontmatter splits a `---\nyaml\n---\nbody` document into SkillMeta + body text.
// Returns an error when the file has no frontmatter delimiters, or when the parsed YAML
// is missing the required name or description field.
func parseFrontmatter(content []byte) (*SkillMeta, error) {
	const sep = "---"

	s := string(content)

	// Must start with "---" (allow leading whitespace / BOM edge cases)
	trimmed := strings.TrimLeft(s, "\r\n\t ")
	if !strings.HasPrefix(trimmed, sep) {
		return nil, fmt.Errorf("no frontmatter found: file does not start with ---")
	}

	// Locate the opening "---"
	first := strings.Index(s, sep)
	// Skip past it and consume the trailing newline
	after := s[first+len(sep):]
	nl := strings.IndexAny(after, "\n")
	if nl < 0 {
		return nil, fmt.Errorf("no frontmatter found: no content after opening ---")
	}
	rest := after[nl+1:]

	// Locate the closing "---"
	closing := strings.Index(rest, sep)
	if closing < 0 {
		return nil, fmt.Errorf("no frontmatter found: missing closing ---")
	}

	yamlPart := rest[:closing]
	body := strings.TrimLeft(rest[closing+len(sep):], "\r\n")

	var meta SkillMeta
	if err := yaml.Unmarshal([]byte(yamlPart), &meta); err != nil {
		return nil, fmt.Errorf("invalid YAML frontmatter: %w", err)
	}

	if meta.Name == "" {
		return nil, fmt.Errorf("frontmatter missing required field: name")
	}
	if meta.Description == "" {
		return nil, fmt.Errorf("frontmatter missing required field: description")
	}

	meta.Body = body
	return &meta, nil
}

// placeholder FuncMap used during initial template parsing so that template.Parse
// does not fail on unknown function names. Real functions are injected per-render
// via cloning.
func placeholderFuncMap() template.FuncMap {
	return template.FuncMap{
		"CallerHasSkill": func(name string) bool { return false },
		"JoinCLIs":       func() string { return "" },
		"RoleFor":        func(role string) string { return "unknown" },
		"HasCLI":         func(name string) bool { return false },
	}
}

// Load walks embedded (required) and disk (optional) file systems, parses skill
// markdown files, and compiles all bodies into a single template set.
//
// Rules:
//   - Embedded FS is walked first.
//   - Disk files with the same slug override embedded ones (logged as a warning).
//   - Files whose basename starts with '_' are fragments (IsFragment=true).
//   - Files where IsPrompt() returns false are also marked IsFragment=true.
//   - All skill bodies and fragments are registered as named templates by slug.
func (e *Engine) Load(embedded fs.FS, diskDir string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	skills := make(map[string]*SkillMeta)

	// Root template set — all skill bodies are registered here as named templates.
	root := template.New("").Option("missingkey=zero").Funcs(placeholderFuncMap())

	loadFS := func(fsys fs.FS, override bool) error {
		return fs.WalkDir(fsys, ".", func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".md") {
				return nil
			}

			raw, err := fs.ReadFile(fsys, path)
			if err != nil {
				return fmt.Errorf("read %s: %w", path, err)
			}

			meta, err := parseFrontmatter(raw)
			if err != nil {
				return fmt.Errorf("parse %s: %w", path, err)
			}

			meta.FilePath = path

			base := d.Name() // e.g. "_helper.md" or "debug.md"
			slug := strings.TrimSuffix(base, ".md")

			meta.IsFragment = strings.HasPrefix(base, "_") || !meta.IsPrompt()

			if override {
				if _, exists := skills[slug]; exists {
					log.Printf("skill %q overridden by disk: %s", slug, path)
				}
			}
			skills[slug] = meta

			if _, tmplErr := root.New(slug).Parse(meta.Body); tmplErr != nil {
				return fmt.Errorf("template parse %s: %w", path, tmplErr)
			}

			return nil
		})
	}

	if err := loadFS(embedded, false); err != nil {
		return fmt.Errorf("load embedded skills: %w", err)
	}

	if diskDir != "" {
		if _, statErr := os.Stat(diskDir); statErr == nil {
			diskFSys := os.DirFS(diskDir)
			if err := loadFS(diskFSys, true); err != nil {
				log.Printf("disk skill dir %q error: %v", diskDir, err)
			}
		}
	}

	e.skills = skills
	e.templates = root
	return nil
}

// Render executes the named skill template with the provided SkillData.
// data.RelatedSkills is populated from engine metadata before rendering.
// Template panics are caught and returned as errors rather than crashing.
func (e *Engine) Render(name string, data *SkillData) (out string, renderErr error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	meta, ok := e.skills[name]
	if !ok {
		return "", fmt.Errorf("skill %q not found", name)
	}

	// Populate RelatedSkills from engine metadata.
	related := make([]RelatedSkill, 0, len(meta.Related))
	for _, rel := range meta.Related {
		if rm, found := e.skills[rel]; found {
			related = append(related, RelatedSkill{
				Name:        rm.Name,
				Description: rm.Description,
			})
		}
	}
	data.RelatedSkills = related

	// Build a fresh template set per-render.
	// Option("missingkey=zero") is set on every individual named template because
	// text/template checks the option on the specific template being executed, not the root.
	// Funcs must be called before Parse, so we chain: New(slug).Option(...).Funcs(...).Parse(body).
	funcMap := SkillFuncMap(data)
	set := template.New("").Funcs(funcMap) // root anchor (never executed)
	for slug, sm := range e.skills {
		if _, parseErr := set.New(slug).Option("missingkey=zero").Funcs(funcMap).Parse(sm.Body); parseErr != nil {
			return "", fmt.Errorf("re-parse skill %q: %w", slug, parseErr)
		}
	}

	// Recover from panics produced by invalid template actions (e.g. {{call .BadFunc}}).
	defer func() {
		if r := recover(); r != nil {
			renderErr = fmt.Errorf("template panic in skill %q: %v", name, r)
		}
	}()

	var buf bytes.Buffer
	if execErr := set.ExecuteTemplate(&buf, name, data); execErr != nil {
		return "", fmt.Errorf("execute skill %q: %w", name, execErr)
	}
	return buf.String(), nil
}

// Skills returns copies of all non-fragment, IsPrompt()==true skill metadata.
func (e *Engine) Skills() []SkillMeta {
	e.mu.RLock()
	defer e.mu.RUnlock()

	result := make([]SkillMeta, 0, len(e.skills))
	for _, m := range e.skills {
		if !m.IsFragment && m.IsPrompt() {
			cp := *m
			result = append(result, cp)
		}
	}
	return result
}

// Get returns the SkillMeta for the given slug, or nil if not found.
func (e *Engine) Get(name string) *SkillMeta {
	e.mu.RLock()
	defer e.mu.RUnlock()

	m, ok := e.skills[name]
	if !ok {
		return nil
	}
	return m
}
