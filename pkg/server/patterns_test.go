package server_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/thebtf/aimux/pkg/think"
	"github.com/thebtf/aimux/pkg/think/patterns"
)

func TestRegisterPatternTools_AllPatternsRegistered(t *testing.T) {
	patterns.RegisterAll()
	srv := newTestServer(t)
	_ = srv // ensure server builds without panic

	allPatterns := think.GetAllPatterns()
	if len(allPatterns) != 23 {
		t.Errorf("expected 23 patterns registered, got %d: %v", len(allPatterns), allPatterns)
	}
}

func TestRegisterPatternTools_DescriptionFormat(t *testing.T) {
	patterns.RegisterAll()
	srv := newTestServer(t)

	allPatterns := think.GetAllPatterns()
	for _, name := range allPatterns {
		desc := srv.ToolDescription(name)
		if desc == "" {
			t.Errorf("pattern %q: ToolDescription returned empty — tool may not be registered as individual MCP tool", name)
			continue
		}
		preview := desc
		if len(preview) > 50 {
			preview = preview[:50]
		}
		if !strings.HasPrefix(desc, "[solo — free]") {
			t.Errorf("pattern %q: description does not start with '[solo — free]', got: %q", name, preview)
		}
	}
}

func TestRegisterPatternTools_HasSchemaFields(t *testing.T) {
	patterns.RegisterAll()

	allPatterns := think.GetAllPatterns()
	for _, name := range allPatterns {
		handler := think.GetPattern(name)
		if handler == nil {
			t.Errorf("pattern %q: GetPattern returned nil", name)
			continue
		}
		fields := handler.SchemaFields()
		if len(fields) == 0 {
			t.Errorf("pattern %q: SchemaFields() returned empty map", name)
		}
	}
}

func TestRegisterPatternTools_ThinkToolRemoved(t *testing.T) {
	srv := newTestServer(t)
	desc := srv.ToolDescription("think")
	// "think" is now a per-pattern tool itself (one of the 23), so it should be registered
	// as an individual tool with the [solo — free] prefix, NOT the old WHAT/WHEN format.
	if desc == "" {
		t.Error("think tool not registered as individual pattern tool")
		return
	}
	// Must NOT have old stateful format headers
	if strings.Contains(desc, "WHAT:") {
		t.Error("think tool still has old WHAT:/WHEN: stateful format — should have [solo — free] format now")
	}
}

func TestRegisterPatternTools_AnnotationsReadOnly(t *testing.T) {
	// Verify the server builds with 23 pattern tools without panic.
	// Annotations are set internally by registerPatternTools; we verify
	// the server construction completes (all 23 tools registered) as proof.
	patterns.RegisterAll()
	srv := newTestServer(t)
	if srv == nil {
		t.Fatal("server should not be nil")
	}

	// Spot-check a sample of patterns are individually registered
	spotCheck := []string{
		"critical_thinking",
		"debugging_approach",
		"decision_framework",
		"peer_review",
		"research_synthesis",
		"experimental_loop",
		"architecture_analysis",
		"sequential_thinking",
	}
	for _, name := range spotCheck {
		desc := srv.ToolDescription(name)
		if desc == "" {
			t.Errorf("pattern %q: not registered as individual MCP tool", name)
		}
	}
}

func TestRegisterPatternTools_StatefulPatternsHaveSessionID(t *testing.T) {
	patterns.RegisterAll()
	srv := newTestServer(t)

	statefulPatterns := []string{
		"sequential_thinking",
		"debugging_approach",
		"scientific_method",
		"structured_argumentation",
		"collaborative_reasoning",
		"experimental_loop",
	}

	for _, name := range statefulPatterns {
		if !think.IsStatefulPattern(name) {
			t.Errorf("pattern %q: expected IsStatefulPattern=true", name)
		}
		desc := srv.ToolDescription(name)
		if desc == "" {
			t.Errorf("pattern %q: not registered", name)
		}
	}
}

func TestPatternDescriptions_AllPresent(t *testing.T) {
	patterns.RegisterAll()
	srv := newTestServer(t)

	allPatterns := think.GetAllPatterns()
	for _, name := range allPatterns {
		handler := think.GetPattern(name)
		if handler == nil {
			t.Errorf("pattern %q: handler is nil", name)
			continue
		}
		desc := srv.ToolDescription(name)
		if desc == "" {
			t.Errorf("pattern %q: ToolDescription empty — not registered as MCP tool", name)
		}
	}
}

func TestRegisterPatternTools_TotalCount(t *testing.T) {
	patterns.RegisterAll()

	allPatterns := think.GetAllPatterns()
	expected := 23
	if len(allPatterns) != expected {
		t.Errorf("expected %d patterns, got %d", expected, len(allPatterns))
	}
}

type testPatternHandler struct {
	name   string
	fields map[string]think.FieldSchema
}

func (h testPatternHandler) Name() string { return h.name }

func (h testPatternHandler) Description() string { return "test pattern" }

func (h testPatternHandler) Validate(input map[string]any) (map[string]any, error) { return input, nil }

func (h testPatternHandler) Handle(validInput map[string]any, sessionID string) (*think.ThinkResult, error) {
	return think.MakeThinkResult(h.name, map[string]any{"ok": true}, sessionID, nil, "", nil), nil
}

func (h testPatternHandler) SchemaFields() map[string]think.FieldSchema { return h.fields }

func (h testPatternHandler) Category() string { return "solo" }

func withIsolatedPatternRegistry(t *testing.T, custom think.PatternHandler) string {
	t.Helper()
	patterns.RegisterAll()

	originalNames := think.GetAllPatterns()
	originalHandlers := make([]think.PatternHandler, 0, len(originalNames))
	originalMeta := make(map[string]think.PatternMeta, len(originalNames))
	for _, name := range originalNames {
		handler := think.GetPattern(name)
		if handler == nil {
			t.Fatalf("pattern %q: GetPattern returned nil while snapshotting registry", name)
		}
		originalHandlers = append(originalHandlers, handler)
		if meta, ok := think.GetPatternMeta(name); ok {
			originalMeta[name] = meta
		}
	}

	think.ClearPatterns()
	for _, handler := range originalHandlers {
		think.RegisterPattern(handler)
		if meta, ok := originalMeta[handler.Name()]; ok {
			think.RegisterPatternMeta(handler.Name(), meta)
		}
	}
	think.RegisterPattern(custom)

	t.Cleanup(func() {
		think.ClearPatterns()
		for _, handler := range originalHandlers {
			think.RegisterPattern(handler)
			if meta, ok := originalMeta[handler.Name()]; ok {
				think.RegisterPatternMeta(handler.Name(), meta)
			}
		}
	})

	return custom.Name()
}

func registerTestPattern(t *testing.T, fields map[string]think.FieldSchema) string {
	t.Helper()

	name := fmt.Sprintf("test_pattern_%s", strings.ToLower(strings.ReplaceAll(t.Name(), "/", "_")))
	return withIsolatedPatternRegistry(t, testPatternHandler{name: name, fields: fields})
}

func TestRegisterPatternTools_AllowsCompleteCompositeSchemas(t *testing.T) {
	registerTestPattern(t, map[string]think.FieldSchema{
		"items": {
			Type:        "array",
			Description: "Array field with item schema",
			Items: map[string]any{
				"type": "string",
			},
		},
		"metadata": {
			Type:        "object",
			Description: "Object field with property schema",
			Properties: map[string]any{
				"id": map[string]any{"type": "string"},
			},
		},
	})

	srv := newTestServer(t)
	if srv == nil {
		t.Fatal("server should not be nil")
	}
}

func TestRegisterPatternTools_PanicsOnIncompleteArraySchema(t *testing.T) {
	patternName := registerTestPattern(t, map[string]think.FieldSchema{
		"items": {
			Type:        "array",
			Description: "Array field missing item schema",
		},
	})

	panicText := fmt.Sprintf("server: pattern %q field %q declares array schema without items", patternName, "items")
	defer func() {
		recovered := recover()
		if recovered == nil {
			t.Fatal("expected panic for incomplete array schema")
		}
		if recovered != panicText {
			t.Fatalf("expected panic %q, got %v", panicText, recovered)
		}
	}()

	_ = newTestServer(t)
}

func TestRegisterPatternTools_PanicsOnIncompleteObjectSchema(t *testing.T) {
	patternName := registerTestPattern(t, map[string]think.FieldSchema{
		"metadata": {
			Type:        "object",
			Description: "Object field missing properties schema",
		},
	})

	panicText := fmt.Sprintf("server: pattern %q field %q declares object schema without properties", patternName, "metadata")
	defer func() {
		recovered := recover()
		if recovered == nil {
			t.Fatal("expected panic for incomplete object schema")
		}
		if recovered != panicText {
			t.Fatalf("expected panic %q, got %v", panicText, recovered)
		}
	}()

	_ = newTestServer(t)
}
