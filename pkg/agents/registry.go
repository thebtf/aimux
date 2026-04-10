// Package agents implements the Agent registry with multi-source discovery.
// Agents are first-class workflow objects (FR-13) with prompts, tool whitelists,
// context sources, success criteria, max_turns, and escalation rules.
package agents

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Agent represents a discovered agent definition.
type Agent struct {
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Role        string            `json:"role,omitempty" yaml:"role,omitempty"`
	Model       string            `json:"model,omitempty" yaml:"model,omitempty"`
	Effort      string            `json:"effort,omitempty" yaml:"effort,omitempty"`
	Timeout     int               `json:"timeout,omitempty" yaml:"timeout,omitempty"`
	Domain      string            `json:"domain,omitempty"`
	Source      string            `json:"source"` // file path or source identifier
	Content     string            `json:"content,omitempty"`
	Tools       []string          `json:"tools,omitempty"`
	MaxTurns    int               `json:"max_turns,omitempty"`
	Meta        map[string]string `json:"meta,omitempty"`
}

// Registry discovers and manages agents from multiple sources.
type Registry struct {
	agents map[string]*Agent
	mu     sync.RWMutex
}

// NewRegistry creates an empty agent registry.
func NewRegistry() *Registry {
	return &Registry{
		agents: make(map[string]*Agent),
	}
}

// Discover scans configured directories for agent definitions.
// Sources (9 total per spec): project .aimux/, .claude/, .codex/, .claw/,
// user-level equivalents, config/agents/, built-in.
// Order: plugin → user → project (project agents shadow user, user shadows plugin).
func (r *Registry) Discover(projectDir string, userDir string) {
	// Plugin-level sources (lowest priority — shadowed by user and project)
	r.discoverPluginAgents(userDir)

	// User-level sources
	userSources := []string{
		filepath.Join(userDir, ".aimux", "agents"),
		filepath.Join(userDir, ".claude", "agents"),
	}

	// Project-level sources
	projectSources := []string{
		filepath.Join(projectDir, ".aimux", "agents"),
		filepath.Join(projectDir, ".claude", "agents"),
		filepath.Join(projectDir, ".codex", "agents"),
		filepath.Join(projectDir, ".claw", "agents"),
	}

	// Scan all sources (project sources shadow user sources, user sources shadow plugins)
	for _, dir := range userSources {
		r.scanDir(dir, "user")
	}
	for _, dir := range projectSources {
		r.scanDir(dir, "project") // project overrides user
	}
}

// pluginManifest represents the structure of installed_plugins.json.
type pluginManifest struct {
	Version int                        `json:"version"`
	Plugins map[string][]pluginInstall `json:"plugins"`
}

// pluginInstall represents a single installed plugin entry.
type pluginInstall struct {
	Scope       string `json:"scope"`
	InstallPath string `json:"installPath"`
	Version     string `json:"version"`
}

// discoverPluginAgents scans ~/.claude/plugins/installed_plugins.json and
// loads agents from each installed plugin. Agents are namespaced as
// "{pluginName}:{agentName}" to match Claude Code's naming convention.
func (r *Registry) discoverPluginAgents(userDir string) {
	manifestPath := filepath.Join(userDir, ".claude", "plugins", "installed_plugins.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return // manifest not present — silent skip
	}

	var manifest pluginManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return // corrupt manifest — silent skip
	}

	for pluginKey, installs := range manifest.Plugins {
		if len(installs) == 0 {
			continue
		}
		install := installs[0]
		if install.InstallPath == "" {
			continue
		}

		// Extract plugin name: "nvmd-platform@nvmd-ai-kit" → "nvmd-platform"
		pluginName := pluginKey
		if idx := strings.Index(pluginKey, "@"); idx >= 0 {
			pluginName = pluginKey[:idx]
		}

		// Pattern A: {installPath}/agents/*.md
		r.scanPluginDir(
			filepath.Join(install.InstallPath, "agents"),
			pluginName,
			install.Version,
			pluginName+":",
		)

		// Pattern B: {installPath}/skills/*/agents/*.md
		skillGlob := filepath.Join(install.InstallPath, "skills", "*", "agents")
		skillDirs, _ := filepath.Glob(skillGlob)
		for _, dir := range skillDirs {
			// extract skillName from path: .../skills/skill-creator/agents → "skill-creator"
			parent := filepath.Dir(dir) // .../skills/skill-creator
			skillName := filepath.Base(parent)
			r.scanPluginDir(
				dir,
				pluginName,
				install.Version,
				pluginName+":"+skillName+":",
			)
		}
	}
}

// scanPluginDir reads all .md files in dir and registers them as plugin agents.
// The agent map key is agentNamePrefix+baseName (e.g. "nvmd-platform:debugger").
func (r *Registry) scanPluginDir(dir, pluginName, pluginVersion, agentNamePrefix string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return // directory doesn't exist — skip silently
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}

		path := filepath.Join(dir, entry.Name())
		content, err := os.ReadFile(path)
		if err != nil {
			continue
		}

		baseName := strings.TrimSuffix(entry.Name(), ".md")
		fullName := agentNamePrefix + baseName
		agent := &Agent{
			Name:    fullName,
			Source:  path,
			Content: string(content),
			Meta: map[string]string{
				"source_type":    "plugin",
				"plugin":         pluginName,
				"plugin_version": pluginVersion,
			},
		}

		// Parse YAML frontmatter if present
		parseFrontmatter(agent, string(content))

		r.mu.Lock()
		r.agents[fullName] = agent
		r.mu.Unlock()
	}
}

// scanDir reads all .md files in a directory as agent definitions.
func (r *Registry) scanDir(dir, source string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return // directory doesn't exist — skip silently
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}

		path := filepath.Join(dir, entry.Name())
		content, err := os.ReadFile(path)
		if err != nil {
			continue
		}

		name := strings.TrimSuffix(entry.Name(), ".md")
		agent := &Agent{
			Name:    name,
			Source:  path,
			Content: string(content),
			Meta:    map[string]string{"source_type": source},
		}

		// Parse YAML frontmatter if present
		parseFrontmatter(agent, string(content))

		r.mu.Lock()
		r.agents[name] = agent
		r.mu.Unlock()
	}
}

// Get returns an agent by name.
func (r *Registry) Get(name string) (*Agent, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	agent, ok := r.agents[name]
	if !ok {
		return nil, fmt.Errorf("agent %q not found", name)
	}
	return agent, nil
}

// List returns all registered agents.
func (r *Registry) List() []*Agent {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]*Agent, 0, len(r.agents))
	for _, a := range r.agents {
		result = append(result, a)
	}
	return result
}

// Find searches agents by keyword matching on name, description, domain, role, and content.
func (r *Registry) Find(query string) []*Agent {
	r.mu.RLock()
	defer r.mu.RUnlock()

	query = strings.ToLower(query)
	var matches []*Agent

	for _, a := range r.agents {
		// Search content prefix (first ~200 runes) to avoid scanning huge files
		contentPrefix := a.Content
		if len(contentPrefix) > 200 {
			// Truncate at rune boundary to avoid splitting multi-byte chars
			runes := []rune(contentPrefix)
			if len(runes) > 200 {
				contentPrefix = string(runes[:200])
			}
		}

		if strings.Contains(strings.ToLower(a.Name), query) ||
			strings.Contains(strings.ToLower(a.Description), query) ||
			strings.Contains(strings.ToLower(a.Domain), query) ||
			strings.Contains(strings.ToLower(a.Role), query) ||
			strings.Contains(strings.ToLower(contentPrefix), query) {
			matches = append(matches, a)
		}
	}

	return matches
}

// Register manually adds an agent (for built-in agents).
func (r *Registry) Register(agent *Agent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.agents[agent.Name] = agent
}

// parseFrontmatter extracts YAML frontmatter fields from agent content.
func parseFrontmatter(agent *Agent, content string) {
	if !strings.HasPrefix(content, "---\n") {
		return
	}

	end := strings.Index(content[4:], "\n---")
	if end < 0 {
		return
	}

	frontmatter := content[4 : 4+end]
	for _, line := range strings.Split(frontmatter, "\n") {
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])

		switch key {
		case "description":
			agent.Description = val
		case "role":
			agent.Role = val
		case "model":
			agent.Model = val
		case "effort":
			agent.Effort = val
		case "timeout":
			if n, err := fmt.Sscanf(val, "%d", &agent.Timeout); n != 1 || err != nil {
				agent.Timeout = 0
			}
		case "domain":
			agent.Domain = val
		}
	}
}
