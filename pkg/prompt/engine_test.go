package prompt_test

import (
	"testing"

	"github.com/thebtf/aimux/pkg/prompt"
)

func TestEngine_RegisterAndGet(t *testing.T) {
	e := prompt.NewEngine()
	e.Register("test", "Hello {{name}}")

	tmpl, err := e.Get("test")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if tmpl.Name != "test" {
		t.Errorf("Name = %q, want test", tmpl.Name)
	}
}

func TestEngine_ResolveVars(t *testing.T) {
	e := prompt.NewEngine()
	e.Register("greeting", "Hello {{name}}, welcome to {{project}}")

	result, err := e.Resolve("greeting", map[string]string{
		"name":    "World",
		"project": "aimux",
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	expected := "Hello World, welcome to aimux"
	if result != expected {
		t.Errorf("got %q, want %q", result, expected)
	}
}

func TestEngine_ResolveIncludes(t *testing.T) {
	e := prompt.NewEngine()
	e.Register("header", "# System Prompt\n")
	e.Register("main", `{{include "header"}}Do the task: {{task}}`)

	result, err := e.Resolve("main", map[string]string{"task": "write code"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if result != "# System Prompt\nDo the task: write code" {
		t.Errorf("got %q", result)
	}
}

func TestEngine_CircularIncludeProtection(t *testing.T) {
	e := prompt.NewEngine()
	e.Register("a", `{{include "b"}}`)
	e.Register("b", `{{include "a"}}`)

	_, err := e.Resolve("a", nil)
	if err == nil {
		t.Error("expected error for circular include")
	}
}

func TestEngine_NotFound(t *testing.T) {
	e := prompt.NewEngine()
	_, err := e.Get("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent template")
	}
}

func TestEngine_List(t *testing.T) {
	e := prompt.NewEngine()
	e.Register("a", "")
	e.Register("b", "")

	names := e.List()
	if len(names) != 2 {
		t.Errorf("List = %d, want 2", len(names))
	}
}
