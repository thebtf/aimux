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
	Name          string            `json:"name"`
	Description   string            `json:"description,omitempty"`
	Role          string            `json:"role,omitempty" yaml:"role,omitempty"`
	Model         string            `json:"model,omitempty" yaml:"model,omitempty"`
	Effort        string            `json:"effort,omitempty" yaml:"effort,omitempty"`
	Timeout       int               `json:"timeout,omitempty" yaml:"timeout,omitempty"`
	Domain        string            `json:"domain,omitempty"`
	Source        string            `json:"source"` // file path or source identifier
	Content       string            `json:"content,omitempty"`
	ContentPrefix string            `json:"-"` // first 200 runes of Content, pre-computed at registration
	Tools         []string          `json:"tools,omitempty"`
	MaxTurns      int               `json:"max_turns,omitempty" yaml:"max_turns,omitempty"`
	// When describes the conditions under which this agent should be selected.
	// Used by agents(action="find") to match tasks to the right agent.
	When          string            `json:"when,omitempty" yaml:"when,omitempty"`
	Meta          map[string]string `json:"meta,omitempty"`
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

// DiscoverForProject scans a project directory for agent definitions and returns
// them as a slice without modifying the shared registry. Used by SessionHandler
// for per-project agent overlay.
func (r *Registry) DiscoverForProject(projectDir string) []*Agent {
	sources := []string{
		filepath.Join(projectDir, ".aimux", "agents"),
		filepath.Join(projectDir, ".claude", "agents"),
		filepath.Join(projectDir, ".codex", "agents"),
		filepath.Join(projectDir, ".claw", "agents"),
	}

	// Use a temporary registry to collect without mutating the shared one.
	tmp := NewRegistry()
	for _, dir := range sources {
		tmp.scanDir(dir, "project")
	}
	return tmp.List()
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

		// Extract plugin name: strip the trailing @marketplace suffix.
		// LastIndex (not Index) correctly handles npm-style scoped names like
		// "@scope/plugin@marketplace" → "@scope/plugin". Requiring idx > 0
		// preserves a leading "@" in scoped names.
		pluginName := pluginKey
		if idx := strings.LastIndex(pluginKey, "@"); idx > 0 {
			pluginName = pluginKey[:idx]
		}

		// Iterate over every install entry — installed_plugins.json may list
		// the same plugin in multiple scopes (user, project) or versions.
		// Silently skipping [1:] would drop subagents that live in a
		// project-scoped install alongside a user-scoped one.
		for _, install := range installs {
			if install.InstallPath == "" {
				continue
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
		contentStr := string(content)
		agent := &Agent{
			Name:          fullName,
			Source:        path,
			Content:       contentStr,
			ContentPrefix: computeContentPrefix(contentStr),
			Meta: map[string]string{
				"source_type":    "plugin",
				"plugin":         pluginName,
				"plugin_version": pluginVersion,
			},
		}

		// Parse YAML frontmatter if present
		parseFrontmatter(agent, contentStr)

		r.mu.Lock()
		r.agents[fullName] = agent
		r.mu.Unlock()
	}
}

// scanDir reads all .md files in a directory as agent definitions.
// Agents are collected into a local map first, then inserted with a single lock
// acquisition to reduce contention during bulk discovery.
func (r *Registry) scanDir(dir, source string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return // directory doesn't exist — skip silently
	}

	local := make(map[string]*Agent)
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
		contentStr := string(content)
		agent := &Agent{
			Name:          name,
			Source:        path,
			Content:       contentStr,
			ContentPrefix: computeContentPrefix(contentStr),
			Meta:          map[string]string{"source_type": source},
		}

		// Parse YAML frontmatter if present
		parseFrontmatter(agent, contentStr)

		local[name] = agent
	}

	if len(local) == 0 {
		return
	}

	r.mu.Lock()
	for name, agent := range local {
		r.agents[name] = agent
	}
	r.mu.Unlock()
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
		// Use pre-computed ContentPrefix (first 200 runes, set at registration).
		if strings.Contains(strings.ToLower(a.Name), query) ||
			strings.Contains(strings.ToLower(a.Description), query) ||
			strings.Contains(strings.ToLower(a.Domain), query) ||
			strings.Contains(strings.ToLower(a.Role), query) ||
			strings.Contains(strings.ToLower(a.ContentPrefix), query) {
			matches = append(matches, a)
		}
	}

	return matches
}

// Register manually adds an agent (for built-in agents).
// ContentPrefix is computed here if not already set.
func (r *Registry) Register(agent *Agent) {
	if agent.ContentPrefix == "" && agent.Content != "" {
		agent.ContentPrefix = computeContentPrefix(agent.Content)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.agents[agent.Name] = agent
}

// computeContentPrefix returns the first 200 runes of content as a string.
// Pre-computed at registration to avoid per-call []rune allocation during scoring.
func computeContentPrefix(content string) string {
	runes := []rune(content)
	if len(runes) <= 200 {
		return content
	}
	return string(runes[:200])
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
		case "when":
			agent.When = val
		}
	}
}
