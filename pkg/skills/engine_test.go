package skills

import (
	"os"
	"strings"
	"testing"
	"testing/fstest"
)

// skillFile builds a valid skill markdown file with name, description, optional
// extra frontmatter, and a body.
func skillFile(name, description, extra, body string) []byte {
	fm := "---\nname: " + name + "\ndescription: " + description + "\n"
	if extra != "" {
		fm += extra + "\n"
	}
	fm += "---\n" + body
	return []byte(fm)
}

// fragmentFile builds a skill markdown file that is explicitly not a prompt.
func fragmentFile(name, description, body string) []byte {
	return []byte("---\nname: " + name + "\ndescription: " + description + "\nprompt: false\n---\n" + body)
}

// ---- parseFrontmatter --------------------------------------------------------

func TestParseFrontmatter(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		content := []byte("---\nname: debug\ndescription: Debug a program\ntags:\n  - dev\n---\nThis is the body.\n")
		meta, err := parseFrontmatter(content)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if meta.Name != "debug" {
			t.Errorf("Name = %q, want %q", meta.Name, "debug")
		}
		if meta.Description != "Debug a program" {
			t.Errorf("Description = %q, want %q", meta.Description, "Debug a program")
		}
		if !strings.Contains(meta.Body, "This is the body.") {
			t.Errorf("Body missing expected content, got: %q", meta.Body)
		}
		if len(meta.Tags) != 1 || meta.Tags[0] != "dev" {
			t.Errorf("Tags = %v, want [dev]", meta.Tags)
		}
	})

	t.Run("missing-name", func(t *testing.T) {
		content := []byte("---\ndescription: Something\n---\nbody")
		_, err := parseFrontmatter(content)
		if err == nil {
			t.Fatal("expected error for missing name, got nil")
		}
		if !strings.Contains(err.Error(), "name") {
			t.Errorf("error %q should mention 'name'", err.Error())
		}
	})

	t.Run("missing-description", func(t *testing.T) {
		content := []byte("---\nname: foo\n---\nbody")
		_, err := parseFrontmatter(content)
		if err == nil {
			t.Fatal("expected error for missing description, got nil")
		}
		if !strings.Contains(err.Error(), "description") {
			t.Errorf("error %q should mention 'description'", err.Error())
		}
	})

	t.Run("no-frontmatter", func(t *testing.T) {
		content := []byte("Just some markdown without delimiters.\n")
		_, err := parseFrontmatter(content)
		if err == nil {
			t.Fatal("expected error for missing frontmatter, got nil")
		}
	})

	t.Run("no-closing-delimiter", func(t *testing.T) {
		content := []byte("---\nname: foo\ndescription: bar\n")
		_, err := parseFrontmatter(content)
		if err == nil {
			t.Fatal("expected error for missing closing ---, got nil")
		}
	})
}

// ---- Load --------------------------------------------------------------------

func TestEngineLoad(t *testing.T) {
	fsys := fstest.MapFS{
		"debug.md": &fstest.MapFile{
			Data: skillFile("debug", "Debug a program", "", "Debug body {{.CLICount}}"),
		},
		"review.md": &fstest.MapFile{
			Data: skillFile("review", "Review code", "", "Review body"),
		},
		"_helper.md": &fstest.MapFile{
			Data: skillFile("helper", "Internal helper fragment", "", "{{define \"helper\"}}helper content{{end}}"),
		},
	}

	eng := NewEngine()
	if err := eng.Load(fsys, ""); err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	skills := eng.Skills()
	if len(skills) != 2 {
		t.Errorf("Skills() count = %d, want 2 (fragment excluded)", len(skills))
	}

	// Fragment must still be stored internally
	helper := eng.Get("_helper")
	if helper == nil {
		t.Error("Get(\"_helper\") returned nil, expected fragment to be stored")
	} else if !helper.IsFragment {
		t.Error("_helper.IsFragment = false, want true")
	}
}

// ---- Render ------------------------------------------------------------------

func TestEngineRender(t *testing.T) {
	fsys := fstest.MapFS{
		"counter.md": &fstest.MapFile{
			Data: []byte("---\nname: counter\ndescription: Counter skill\n---\nCount: {{.CLICount}}{{if .HasMultipleCLIs}} multi{{end}}"),
		},
	}

	eng := NewEngine()
	if err := eng.Load(fsys, ""); err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	t.Run("multiple-CLIs", func(t *testing.T) {
		data := &SkillData{CLICount: 3, HasMultipleCLIs: true}
		out, err := eng.Render("counter", data)
		if err != nil {
			t.Fatalf("Render() error: %v", err)
		}
		if !strings.Contains(out, "3") {
			t.Errorf("output %q should contain \"3\"", out)
		}
		if !strings.Contains(out, "multi") {
			t.Errorf("output %q should contain \"multi\" when HasMultipleCLIs=true", out)
		}
	})

	t.Run("single-CLI", func(t *testing.T) {
		data := &SkillData{CLICount: 1, HasMultipleCLIs: false}
		out, err := eng.Render("counter", data)
		if err != nil {
			t.Fatalf("Render() error: %v", err)
		}
		if !strings.Contains(out, "1") {
			t.Errorf("output %q should contain \"1\"", out)
		}
		if strings.Contains(out, "multi") {
			t.Errorf("output %q should NOT contain \"multi\" when HasMultipleCLIs=false", out)
		}
	})
}

// ---- Fragment inclusion ------------------------------------------------------

func TestEngineRenderFragment(t *testing.T) {
	fsys := fstest.MapFS{
		"_test-frag.md": &fstest.MapFile{
			Data: []byte("---\nname: test-frag\ndescription: A test fragment\n---\n{{define \"test-frag\"}}FRAGMENT_CONTENT{{end}}"),
		},
		"main.md": &fstest.MapFile{
			Data: []byte("---\nname: main\ndescription: Main skill\n---\nBefore {{template \"test-frag\" .}} After"),
		},
	}

	eng := NewEngine()
	if err := eng.Load(fsys, ""); err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	out, err := eng.Render("main", &SkillData{})
	if err != nil {
		t.Fatalf("Render() error: %v", err)
	}
	if !strings.Contains(out, "FRAGMENT_CONTENT") {
		t.Errorf("output %q should contain fragment content", out)
	}
}

// ---- missingkey=zero ---------------------------------------------------------

func TestEngineRenderMissingKey(t *testing.T) {
	// missingkey=zero applies to map lookups (not struct field access).
	// SkillData.Args is map[string]string — accessing a missing key should render
	// as the zero value ("") rather than returning an error.
	fsys := fstest.MapFS{
		"missing-key.md": &fstest.MapFile{
			Data: []byte("---\nname: missing-key\ndescription: Missing key test\n---\nValue: {{index .Args \"nonexistent_key\"}}"),
		},
	}

	eng := NewEngine()
	if err := eng.Load(fsys, ""); err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	// Args is nil — index on nil map returns zero value ("") with missingkey=zero
	out, err := eng.Render("missing-key", &SkillData{})
	if err != nil {
		t.Errorf("Render() with missing map key returned error %v, want nil (missingkey=zero)", err)
	}
	if !strings.Contains(out, "Value:") {
		t.Errorf("output %q should contain 'Value:'", out)
	}
}

// ---- panic recovery ----------------------------------------------------------

func TestEngineRenderPanic(t *testing.T) {
	fsys := fstest.MapFS{
		"panic-skill.md": &fstest.MapFile{
			// {{call .BadFunc}} panics because BadFunc is nil
			Data: []byte("---\nname: panic-skill\ndescription: Panic test\n---\n{{call .BadFunc}}"),
		},
	}

	eng := NewEngine()
	if err := eng.Load(fsys, ""); err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	_, err := eng.Render("panic-skill", &SkillData{})
	if err == nil {
		t.Fatal("expected error from panicking template, got nil")
	}
}

// ---- disk override -----------------------------------------------------------

func TestEngineDiskOverride(t *testing.T) {
	embedded := fstest.MapFS{
		"debug.md": &fstest.MapFile{
			Data: skillFile("debug", "Debug a program", "", "embedded body"),
		},
	}

	// Write a temporary disk override
	diskDir := t.TempDir()
	overrideContent := skillFile("debug", "Debug a program", "", "disk override body")
	if err := os.WriteFile(diskDir+"/debug.md", overrideContent, 0o644); err != nil {
		t.Fatalf("write disk override: %v", err)
	}

	eng := NewEngine()
	if err := eng.Load(embedded, diskDir); err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	out, err := eng.Render("debug", &SkillData{})
	if err != nil {
		t.Fatalf("Render() error: %v", err)
	}
	if !strings.Contains(out, "disk override body") {
		t.Errorf("output %q should contain disk override body", out)
	}
	if strings.Contains(out, "embedded body") {
		t.Errorf("output %q should NOT contain embedded body after disk override", out)
	}
}

// ---- Skills() excludes fragments --------------------------------------------

func TestEngineSkillsExcludesFragments(t *testing.T) {
	fsys := fstest.MapFS{
		"real-skill.md": &fstest.MapFile{
			Data: skillFile("real-skill", "A real skill", "", "body"),
		},
		"_fragment.md": &fstest.MapFile{
			Data: skillFile("fragment", "A fragment", "", "fragment body"),
		},
		"no-prompt.md": &fstest.MapFile{
			Data: fragmentFile("no-prompt", "Not a prompt", "body"),
		},
	}

	eng := NewEngine()
	if err := eng.Load(fsys, ""); err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	skills := eng.Skills()
	for _, s := range skills {
		if s.IsFragment {
			t.Errorf("Skills() returned fragment: %q", s.Name)
		}
	}
	if len(skills) != 1 {
		t.Errorf("Skills() count = %d, want 1", len(skills))
	}
	if skills[0].Name != "real-skill" {
		t.Errorf("Skills()[0].Name = %q, want %q", skills[0].Name, "real-skill")
	}
}

// ---- Get ---------------------------------------------------------------------

func TestEngineGet(t *testing.T) {
	fsys := fstest.MapFS{
		"debug.md": &fstest.MapFile{
			Data: skillFile("debug", "Debug a program", "", "body"),
		},
	}

	eng := NewEngine()
	if err := eng.Load(fsys, ""); err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	t.Run("found", func(t *testing.T) {
		m := eng.Get("debug")
		if m == nil {
			t.Fatal("Get(\"debug\") returned nil, want skill")
		}
		if m.Name != "debug" {
			t.Errorf("Get(\"debug\").Name = %q, want %q", m.Name, "debug")
		}
	})

	t.Run("not-found", func(t *testing.T) {
		m := eng.Get("nonexistent")
		if m != nil {
			t.Errorf("Get(\"nonexistent\") = %v, want nil", m)
		}
	})
}
