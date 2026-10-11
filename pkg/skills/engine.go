package skills

import (
	"bytes"
	"fmt"
	"io/fs"
	"log"
	"os"
	"sort"
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

// Load compiles public package entrypoints and legacy root-flat skill files.
// Disk candidates override embedded candidates; within either source, a package
// <name>/SKILL.md wins over <name>.md. Package support files are never templates.
// Legacy underscore files and raw _fragments/ templates are disk-only.
func (e *Engine) Load(embedded fs.FS, diskDir string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	type candidate struct {
		fsys        fs.FS
		path        string
		packaged    bool
		rawFragment bool
		meta        *SkillMeta
	}
	candidates := make(map[string]candidate)
	loadFS := func(fsys fs.FS, disk bool) error {
		selected := make(map[string]candidate)
		err := fs.WalkDir(fsys, ".", func(filePath string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if filePath == "." || (disk && (filePath == "_fragments" || strings.HasPrefix(filePath, "_fragments/"))) {
					return nil
				}
				if strings.Contains(filePath, "/") || strings.HasPrefix(d.Name(), "_") {
					return fs.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(d.Name(), ".md") || (!disk && strings.HasPrefix(d.Name(), "_")) {
				return nil
			}

			dir, rest, nested := strings.Cut(filePath, "/")
			rawFragment := disk && dir == "_fragments" && nested
			packaged := nested && rest == "SKILL.md" && !rawFragment
			if nested && !packaged && !rawFragment {
				return nil
			}
			slug := strings.TrimSuffix(d.Name(), ".md")
			if packaged {
				slug = dir
			}
			c := candidate{fsys: fsys, path: filePath, packaged: packaged, rawFragment: rawFragment}
			if disk && !c.rawFragment {
				raw, err := fs.ReadFile(c.fsys, c.path)
				if err != nil {
					log.Printf("skip %s: %v", c.path, err)
					return nil
				}
				c.meta, err = parseFrontmatter(raw)
				if err != nil {
					log.Printf("skip %s: %v", c.path, err)
					return nil
				}
				if c.packaged && c.meta.Name != slug {
					log.Printf("skip %s: frontmatter name %q does not match package name %q", c.path, c.meta.Name, slug)
					return nil
				}
				if _, err := template.New(slug).Option("missingkey=zero").Funcs(placeholderFuncMap()).Parse(c.meta.Body); err != nil {
					log.Printf("skip %s: template parse: %v", c.path, err)
					return nil
				}
			}
			if previous, exists := selected[slug]; exists && previous.packaged && !packaged {
				return nil
			}
			selected[slug] = c
			return nil
		})
		if err != nil {
			return err
		}
		for slug, c := range selected {
			if disk {
				if _, exists := candidates[slug]; exists {
					log.Printf("skill %q overridden by disk: %s", slug, c.path)
				}
			}
			candidates[slug] = c
		}
		return nil
	}

	if err := loadFS(embedded, false); err != nil {
		return fmt.Errorf("load embedded skills: %w", err)
	}
	if diskDir != "" {
		if _, statErr := os.Stat(diskDir); statErr == nil {
			if err := loadFS(os.DirFS(diskDir), true); err != nil {
				log.Printf("load disk skills %q: %v", diskDir, err)
			}
		}
	}

	slugs := make([]string, 0, len(candidates))
	for slug := range candidates {
		slugs = append(slugs, slug)
	}
	sort.Strings(slugs)
	skills := make(map[string]*SkillMeta, len(candidates))
	root := template.New("").Option("missingkey=zero").Funcs(placeholderFuncMap())
	for _, slug := range slugs {
		c := candidates[slug]
		meta := c.meta
		if meta == nil {
			raw, err := fs.ReadFile(c.fsys, c.path)
			if err != nil {
				return fmt.Errorf("read %s: %w", c.path, err)
			}
			if c.rawFragment {
				meta = &SkillMeta{Name: slug, Body: string(raw), IsFragment: true}
			} else {
				meta, err = parseFrontmatter(raw)
				if err != nil {
					if c.packaged {
						return fmt.Errorf("parse skill package %s: %w", c.path, err)
					}
					log.Printf("skip %s: %v", c.path, err)
					continue
				}
			}
		}
		if c.packaged && meta.Name != slug {
			return fmt.Errorf("skill package %s: frontmatter name %q does not match package name %q", c.path, meta.Name, slug)
		}
		meta.IsFragment = c.rawFragment || strings.HasPrefix(slug, "_") || !meta.IsPrompt()
		meta.FilePath = c.path
		skills[slug] = meta

		// The executing named template needs its own missing-key option.
		if _, err := root.New(slug).Option("missingkey=zero").Parse(meta.Body); err != nil {
			return fmt.Errorf("template parse %s: %w", c.path, err)
		}
	}

	e.skills = skills
	e.templates = root
	return nil
}

// Render executes the named skill template with the provided SkillData.
// data.RelatedSkills is populated from engine metadata before rendering.
// Template panics are caught and returned as errors rather than crashing.
//
// The cached template set (compiled once at Load time) is cloned per-render so
// that per-call FuncMap values can be injected without re-parsing template bodies.
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

	// Clone the pre-compiled template set and inject per-render funcs.
	// Cloning copies the parsed AST without re-parsing; Funcs() replaces the
	// placeholder functions that were installed at Load() time.
	funcMap := SkillFuncMap(data)
	set, cloneErr := e.templates.Clone()
	if cloneErr != nil {
		return "", fmt.Errorf("clone template set: %w", cloneErr)
	}
	set.Funcs(funcMap)

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

// ValidateMap reads _map.yaml from loaded skills and validates consistency
// between the map and the actual loaded skill templates.
func (e *Engine) ValidateMap() []string {
	e.mu.RLock()
	defer e.mu.RUnlock()

	mapMeta, ok := e.skills["_map"]
	if !ok {
		return []string{"_map.yaml not loaded — skip validation"}
	}

	gm, err := ParseGraphMap([]byte(mapMeta.Body))
	if err != nil {
		return []string{fmt.Sprintf("_map.yaml parse error: %v", err)}
	}

	return ValidateMap(gm, e.skills)
}
