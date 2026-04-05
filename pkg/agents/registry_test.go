package agents_test

import (
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
