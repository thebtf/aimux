package agents_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/thebtf/aimux/pkg/agents"
)

func TestRegistry_RegisterAndGet(t *testing.T) {
	reg := agents.NewRegistry()

	reg.Register(&agents.Agent{
		Name:        "test-agent",
		Description: "A test agent",
		Role:        "coding",
	})

	agent, err := reg.Get("test-agent")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if agent.Name != "test-agent" {
		t.Errorf("Name = %q, want test-agent", agent.Name)
	}
}

func TestRegistry_GetNotFound(t *testing.T) {
	reg := agents.NewRegistry()
	_, err := reg.Get("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent agent")
	}
}

func TestRegistry_Find(t *testing.T) {
	reg := agents.NewRegistry()
	reg.Register(&agents.Agent{Name: "code-reviewer", Description: "Reviews code quality"})
	reg.Register(&agents.Agent{Name: "test-writer", Description: "Writes unit tests"})
	reg.Register(&agents.Agent{Name: "doc-gen", Description: "Generates documentation"})

	matches := reg.Find("code")
	if len(matches) != 1 {
		t.Errorf("Find('code') = %d matches, want 1", len(matches))
	}

	matches = reg.Find("test")
	if len(matches) != 1 {
		t.Errorf("Find('test') = %d matches, want 1", len(matches))
	}
}

func TestRegistry_List(t *testing.T) {
	reg := agents.NewRegistry()
	reg.Register(&agents.Agent{Name: "a"})
	reg.Register(&agents.Agent{Name: "b"})

	all := reg.List()
	if len(all) != 2 {
		t.Errorf("List = %d, want 2", len(all))
	}
}

func TestRegistry_DiscoverNonexistentDir(t *testing.T) {
	reg := agents.NewRegistry()
	// Should not panic on nonexistent directories
	reg.Discover("/nonexistent/project", "/nonexistent/user")
	if len(reg.List()) != 0 {
		t.Error("expected 0 agents from nonexistent dirs")
	}
}

// makePluginManifest marshals a minimal installed_plugins.json manifest to JSON.
func makePluginManifest(t *testing.T, plugins map[string][]map[string]string) []byte {
	t.Helper()
	type installEntry struct {
		Scope       string `json:"scope"`
		InstallPath string `json:"installPath"`
		Version     string `json:"version"`
	}
	type manifest struct {
		Version int                       `json:"version"`
		Plugins map[string][]installEntry `json:"plugins"`
	}

	m := manifest{
		Version: 2,
		Plugins: make(map[string][]installEntry),
	}
	for key, entries := range plugins {
		var installs []installEntry
		for _, e := range entries {
			installs = append(installs, installEntry{
				Scope:       e["scope"],
				InstallPath: e["installPath"],
				Version:     e["version"],
			})
		}
		m.Plugins[key] = installs
	}

	data, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	return data
}

func TestRegistry_DiscoverPluginAgents(t *testing.T) {
	userDir := t.TempDir()
	installPath := filepath.Join(userDir, "install", "test-plugin")

	// Create agents directory with a test agent file
	agentsDir := filepath.Join(installPath, "agents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	agentContent := "---\ndescription: Test agent\n---\nBody"
	if err := os.WriteFile(filepath.Join(agentsDir, "foo.md"), []byte(agentContent), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Write installed_plugins.json
	pluginsDir := filepath.Join(userDir, ".claude", "plugins")
	if err := os.MkdirAll(pluginsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	manifest := makePluginManifest(t, map[string][]map[string]string{
		"test-plugin@test-marketplace": {
			{"scope": "user", "installPath": installPath, "version": "1.0.0"},
		},
	})
	if err := os.WriteFile(filepath.Join(pluginsDir, "installed_plugins.json"), manifest, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	reg := agents.NewRegistry()
	reg.Discover("", userDir)

	agent, err := reg.Get("test-plugin:foo")
	if err != nil {
		t.Fatalf("Get(test-plugin:foo): %v", err)
	}
	if agent.Meta["plugin"] != "test-plugin" {
		t.Errorf("Meta[plugin] = %q, want %q", agent.Meta["plugin"], "test-plugin")
	}
	if agent.Meta["source_type"] != "plugin" {
		t.Errorf("Meta[source_type] = %q, want %q", agent.Meta["source_type"], "plugin")
	}
	if agent.Description != "Test agent" {
		t.Errorf("Description = %q, want %q", agent.Description, "Test agent")
	}
}

func TestRegistry_DiscoverPluginAgents_MissingManifest(t *testing.T) {
	userDir := t.TempDir()
	// No installed_plugins.json — should not panic and load zero plugin agents

	reg := agents.NewRegistry()
	reg.Discover("", userDir)

	all := reg.List()
	for _, a := range all {
		if a.Meta != nil && a.Meta["source_type"] == "plugin" {
			t.Errorf("expected no plugin agents, got %q", a.Name)
		}
	}
}

func TestRegistry_DiscoverPluginAgents_NestedSkills(t *testing.T) {
	userDir := t.TempDir()
	installPath := filepath.Join(userDir, "install", "test-plugin")

	// Pattern B: {installPath}/skills/skill-a/agents/bar.md
	skillAgentsDir := filepath.Join(installPath, "skills", "skill-a", "agents")
	if err := os.MkdirAll(skillAgentsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillAgentsDir, "bar.md"), []byte("# bar agent"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	pluginsDir := filepath.Join(userDir, ".claude", "plugins")
	if err := os.MkdirAll(pluginsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	manifest := makePluginManifest(t, map[string][]map[string]string{
		"test-plugin@test-marketplace": {
			{"scope": "user", "installPath": installPath, "version": "1.0.0"},
		},
	})
	if err := os.WriteFile(filepath.Join(pluginsDir, "installed_plugins.json"), manifest, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	reg := agents.NewRegistry()
	reg.Discover("", userDir)

	if _, err := reg.Get("test-plugin:skill-a:bar"); err != nil {
		t.Fatalf("Get(test-plugin:skill-a:bar): %v", err)
	}
}

func TestRegistry_DiscoverPluginAgents_InvalidJSON(t *testing.T) {
	userDir := t.TempDir()

	pluginsDir := filepath.Join(userDir, ".claude", "plugins")
	if err := os.MkdirAll(pluginsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pluginsDir, "installed_plugins.json"), []byte("{bad"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	reg := agents.NewRegistry()
	// Must not panic
	reg.Discover("", userDir)

	all := reg.List()
	for _, a := range all {
		if a.Meta != nil && a.Meta["source_type"] == "plugin" {
			t.Errorf("expected no plugin agents on invalid JSON, got %q", a.Name)
		}
	}
}
