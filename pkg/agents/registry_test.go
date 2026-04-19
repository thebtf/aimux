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

func TestRegistry_DiscoverPluginAgents_ScopedPluginName(t *testing.T) {
	userDir := t.TempDir()
	installPath := filepath.Join(userDir, "install", "scoped-plugin")

	agentsDir := filepath.Join(installPath, "agents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(agentsDir, "helper.md"), []byte("# helper"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	pluginsDir := filepath.Join(userDir, ".claude", "plugins")
	if err := os.MkdirAll(pluginsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	// Scoped plugin key: "@vendor/tools@marketplace" → expect name "@vendor/tools"
	manifest := makePluginManifest(t, map[string][]map[string]string{
		"@vendor/tools@marketplace": {
			{"scope": "user", "installPath": installPath, "version": "1.0.0"},
		},
	})
	if err := os.WriteFile(filepath.Join(pluginsDir, "installed_plugins.json"), manifest, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	reg := agents.NewRegistry()
	reg.Discover("", userDir)

	// The leading "@" must be preserved; the trailing "@marketplace" must be stripped.
	want := "@vendor/tools:helper"
	if _, err := reg.Get(want); err != nil {
		t.Fatalf("Get(%q): %v — scoped plugin name was not preserved", want, err)
	}
}

func TestRegistry_DiscoverPluginAgents_MultipleInstalls(t *testing.T) {
	userDir := t.TempDir()
	installA := filepath.Join(userDir, "install", "a")
	installB := filepath.Join(userDir, "install", "b")

	// Install A has agent foo.md, install B has agent bar.md
	for _, pair := range []struct{ dir, name string }{
		{installA, "foo"},
		{installB, "bar"},
	} {
		agentsDir := filepath.Join(pair.dir, "agents")
		if err := os.MkdirAll(agentsDir, 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := os.WriteFile(filepath.Join(agentsDir, pair.name+".md"), []byte("# "+pair.name), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}

	pluginsDir := filepath.Join(userDir, ".claude", "plugins")
	if err := os.MkdirAll(pluginsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	manifest := makePluginManifest(t, map[string][]map[string]string{
		"multi-plugin@marketplace": {
			{"scope": "user", "installPath": installA, "version": "1.0.0"},
			{"scope": "project", "installPath": installB, "version": "2.0.0"},
		},
	})
	if err := os.WriteFile(filepath.Join(pluginsDir, "installed_plugins.json"), manifest, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	reg := agents.NewRegistry()
	reg.Discover("", userDir)

	// Both installs must be scanned, not just installs[0].
	if _, err := reg.Get("multi-plugin:foo"); err != nil {
		t.Errorf("Get(multi-plugin:foo) from installs[0]: %v", err)
	}
	if _, err := reg.Get("multi-plugin:bar"); err != nil {
		t.Errorf("Get(multi-plugin:bar) from installs[1]: %v", err)
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

// TestList_FiltersDeletedSources verifies that List purges agents whose source
// files have been removed after registration and excludes them from the result.
func TestList_FiltersDeletedSources(t *testing.T) {
	tmpDir := t.TempDir()

	// Write 3 agent files with valid frontmatter.
	for _, name := range []string{"a", "b", "c"} {
		content := "---\ndescription: agent " + name + "\n---\nBody"
		path := filepath.Join(tmpDir, name+".md")
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("WriteFile(%s): %v", name, err)
		}
	}

	// Register agents manually (scanDir is unexported).
	reg := agents.NewRegistry()
	for _, name := range []string{"a", "b", "c"} {
		path := filepath.Join(tmpDir, name+".md")
		content, _ := os.ReadFile(path)
		reg.Register(&agents.Agent{
			Name:        name,
			Description: "agent " + name,
			Source:      path,
			Content:     string(content),
		})
	}

	// Delete b.md to simulate post-boot deletion.
	if err := os.Remove(filepath.Join(tmpDir, "b.md")); err != nil {
		t.Fatalf("Remove(b.md): %v", err)
	}

	all := reg.List()
	if len(all) != 2 {
		t.Errorf("List() returned %d agents, want 2", len(all))
	}
	for _, a := range all {
		if a.Name == "b" {
			t.Errorf("List() included stale agent %q", a.Name)
		}
	}

	// Get("b") must return NotFound after List purged it.
	if _, err := reg.Get("b"); err == nil {
		t.Error("Get(b): expected error after source deleted, got nil")
	}
}

// TestGet_PurgesStaleOnRead verifies that Get removes the stale entry from the
// map and that a subsequent List confirms the map shrank.
func TestGet_PurgesStaleOnRead(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "x.md")
	if err := os.WriteFile(path, []byte("---\ndescription: x\n---"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	reg := agents.NewRegistry()
	reg.Register(&agents.Agent{Name: "x", Source: path})

	// Confirm it's present.
	if _, err := reg.Get("x"); err != nil {
		t.Fatalf("Get(x) before delete: %v", err)
	}

	// Delete the backing file.
	if err := os.Remove(path); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	// Get must return NotFound and purge the entry.
	if _, err := reg.Get("x"); err == nil {
		t.Error("Get(x) after delete: expected error, got nil")
	}

	// List must now show 0 agents (purge already happened in Get).
	if n := len(reg.List()); n != 0 {
		t.Errorf("List() after Get purge: got %d, want 0", n)
	}
}

// TestFind_FiltersDeletedSources verifies that Find excludes agents whose source
// files have been deleted and purges them from the internal map.
func TestFind_FiltersDeletedSources(t *testing.T) {
	tmpDir := t.TempDir()

	writeAgent := func(name, description string) string {
		path := filepath.Join(tmpDir, name+".md")
		content := "---\ndescription: " + description + "\n---"
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("WriteFile(%s): %v", name, err)
		}
		return path
	}

	pathKeep := writeAgent("keep-agent", "matching query text")
	pathDrop := writeAgent("drop-agent", "matching query text")

	reg := agents.NewRegistry()
	reg.Register(&agents.Agent{Name: "keep-agent", Description: "matching query text", Source: pathKeep})
	reg.Register(&agents.Agent{Name: "drop-agent", Description: "matching query text", Source: pathDrop})

	// Delete the source for drop-agent.
	if err := os.Remove(pathDrop); err != nil {
		t.Fatalf("Remove(drop-agent source): %v", err)
	}

	matches := reg.Find("matching query text")
	if len(matches) != 1 {
		t.Errorf("Find() returned %d matches, want 1", len(matches))
	}
	if len(matches) > 0 && matches[0].Name != "keep-agent" {
		t.Errorf("Find() returned %q, want keep-agent", matches[0].Name)
	}
}

// TestBuiltinAgents_NoStatCheck verifies that agents registered with empty Source
// are always included in List/Get results — no os.Stat is attempted for them.
func TestBuiltinAgents_NoStatCheck(t *testing.T) {
	reg := agents.NewRegistry()

	// Register a built-in agent with no filesystem backing.
	reg.Register(&agents.Agent{
		Name:        "builtin",
		Description: "A built-in agent",
		Source:      "", // empty Source — never stat'd
	})

	// List must include the built-in.
	all := reg.List()
	found := false
	for _, a := range all {
		if a.Name == "builtin" {
			found = true
		}
	}
	if !found {
		t.Error("List() did not include built-in agent with empty Source")
	}

	// Get must return the built-in.
	a, err := reg.Get("builtin")
	if err != nil {
		t.Fatalf("Get(builtin): %v", err)
	}
	if a.Name != "builtin" {
		t.Errorf("Get(builtin) returned %q", a.Name)
	}
}
