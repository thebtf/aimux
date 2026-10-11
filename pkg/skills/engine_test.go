package skills

import (
	"os"
	"path/filepath"
	"reflect"
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

	if helper := eng.Get("_helper"); helper != nil {
		t.Errorf("embedded legacy fragment was loaded: %+v", helper)
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
		"main.md": &fstest.MapFile{
			Data: []byte("---\nname: main\ndescription: Main skill\n---\nBefore {{template \"test-frag\" .}} After"),
		},
	}

	diskDir := writeSkillFiles(t, fstest.MapFS{
		"_test-frag.md": &fstest.MapFile{
			Data: []byte("---\nname: test-frag\ndescription: A test fragment\n---\n{{define \"test-frag\"}}FRAGMENT_CONTENT{{end}}"),
		},
	})
	eng := NewEngine()
	if err := eng.Load(fsys, diskDir); err != nil {
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
	fsys := fstest.MapFS{
		"missing-key/SKILL.md": &fstest.MapFile{
			Data: skillFile("missing-key", "Missing key test", "", "Value: [{{.Args.nonexistent_key}}]"),
		},
	}
	eng := NewEngine()
	if err := eng.Load(fsys, ""); err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	for _, args := range []map[string]string{nil, {}} {
		out, err := eng.Render("missing-key", &SkillData{Args: args})
		if err != nil {
			t.Fatalf("Render() with missing map key returned error: %v", err)
		}
		if out != "Value: []" {
			t.Errorf("Render() = %q, want zero-value rendering %q", out, "Value: []")
		}
	}
}

// ---- panic recovery ----------------------------------------------------------

func TestEngineRenderPanic(t *testing.T) {
	fsys := fstest.MapFS{
		"panic-skill.md": &fstest.MapFile{
			// {{call (index .Args "fn")}} panics at runtime because text/template's
			// reflect-based call panics when invoked on a non-func value (string).
			// The deferred recover() in Render catches this and returns it as an error.
			Data: []byte("---\nname: panic-skill\ndescription: Panic test\n---\n{{call (index .Args \"fn\")}}"),
		},
	}

	eng := NewEngine()
	if err := eng.Load(fsys, ""); err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	data := &SkillData{Args: map[string]string{"fn": "not-a-func"}}
	_, err := eng.Render("panic-skill", data)
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

func writeSkillFiles(t *testing.T, files fstest.MapFS) string {
	t.Helper()
	dir := t.TempDir()
	for name, file := range files {
		filePath := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filePath, file.Data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestEnginePackageLoading(t *testing.T) {
	files := fstest.MapFS{
		"debug/SKILL.md":                &fstest.MapFile{Data: skillFile("debug", "Debug a program", "", "Package {{.Args.task}}")},
		"debug/evals/trigger-eval.json": &fstest.MapFile{Data: []byte(`{"name":"eval"}`)},
		"debug/evals/prompt.md":         &fstest.MapFile{Data: skillFile("eval-prompt", "Not public", "", "{{ broken")},
		"debug/references/SKILL.md":     &fstest.MapFile{Data: skillFile("nested", "Not public", "", "{{ broken")},
		"debug/scripts/helper.md":       &fstest.MapFile{Data: skillFile("script", "Not public", "", "{{define \"injected\"}}BAD{{end}}")},
		"debug/notes.md":                &fstest.MapFile{Data: skillFile("notes", "Not public", "", "notes")},
		"debug/other/nested.md":         &fstest.MapFile{Data: skillFile("other", "Not public", "", "other")},
		"_fragments/legacy.md":          &fstest.MapFile{Data: []byte("{{ broken")},
		"_helper.md":                    &fstest.MapFile{Data: skillFile("helper", "Not public", "", "{{ broken")},
	}
	for _, disk := range []bool{false, true} {
		t.Run(map[bool]string{false: "embedded", true: "disk"}[disk], func(t *testing.T) {
			eng := NewEngine()
			embedded := files
			diskDir := ""
			if disk {
				embedded = fstest.MapFS{}
				diskFiles := make(fstest.MapFS)
				for name, file := range files {
					if !strings.HasPrefix(name, "_") {
						diskFiles[name] = file
					}
				}
				diskDir = writeSkillFiles(t, diskFiles)
			}
			if err := eng.Load(embedded, diskDir); err != nil {
				t.Fatal(err)
			}
			public := eng.Skills()
			if len(public) != 1 || public[0].Name != "debug" || public[0].FilePath != "debug/SKILL.md" {
				t.Fatalf("public skills = %+v, want only debug package", public)
			}
			if len(eng.skills) != 1 || eng.Get("SKILL") != nil {
				t.Fatalf("support files became skills: %+v", eng.skills)
			}
			for _, name := range []string{"prompt", "SKILL", "helper", "notes", "nested", "legacy", "_helper", "injected"} {
				if eng.templates.Lookup(name) != nil {
					t.Errorf("support or embedded legacy template %q was registered", name)
				}
			}
			out, err := eng.Render("debug", &SkillData{Args: map[string]string{"task": "argument-marker"}})
			if err != nil || out != "Package argument-marker" {
				t.Fatalf("Render(debug) = %q, %v", out, err)
			}
		})
	}
}

func TestEnginePackagePrecedence(t *testing.T) {
	for _, name := range []string{"debug", "z-debug"} {
		for _, disk := range []bool{false, true} {
			t.Run(name+map[bool]string{false: "/embedded", true: "/disk"}[disk], func(t *testing.T) {
				files := fstest.MapFS{
					name + ".md":       &fstest.MapFile{Data: skillFile(name, "Legacy", "", "{{ broken")},
					name + "/SKILL.md": &fstest.MapFile{Data: skillFile(name, "Package", "", "package winner")},
				}
				embedded := files
				diskDir := ""
				if disk {
					embedded = fstest.MapFS{}
					diskDir = writeSkillFiles(t, files)
				}
				eng := NewEngine()
				if err := eng.Load(embedded, diskDir); err != nil {
					t.Fatal(err)
				}
				if got := eng.Get(name); got == nil || got.FilePath != name+"/SKILL.md" {
					t.Fatalf("Get(%q) = %+v, want package winner", name, got)
				}
				out, err := eng.Render(name, &SkillData{})
				if err != nil || out != "package winner" {
					t.Fatalf("Render(%q) = %q, %v", name, out, err)
				}
			})
		}
	}
}

func TestEnginePackageDiskOverrides(t *testing.T) {
	for _, embeddedPath := range []string{"debug.md", "debug/SKILL.md"} {
		for _, diskPath := range []string{"debug.md", "debug/SKILL.md"} {
			t.Run(embeddedPath+"/"+diskPath, func(t *testing.T) {
				embedded := fstest.MapFS{embeddedPath: &fstest.MapFile{Data: skillFile("debug", "Embedded", "", "embedded")}}
				diskDir := writeSkillFiles(t, fstest.MapFS{diskPath: &fstest.MapFile{Data: skillFile("debug", "Disk", "", "disk winner")}})
				eng := NewEngine()
				if err := eng.Load(embedded, diskDir); err != nil {
					t.Fatal(err)
				}
				out, err := eng.Render("debug", &SkillData{})
				if err != nil || out != "disk winner" {
					t.Fatalf("Render(debug) = %q, %v", out, err)
				}
				if got := eng.Get("debug"); got == nil || got.Description != "Disk" || got.FilePath != diskPath {
					t.Fatalf("disk metadata was lost: %+v", got)
				}
			})
		}
	}
}

func TestEngineRejectsInvalidPackage(t *testing.T) {
	for _, content := range [][]byte{
		skillFile("different", "Wrong identity", "", "body"),
		[]byte("no frontmatter"),
	} {
		for _, source := range []string{"embedded", "disk", "disk-flat"} {
			disk := source != "embedded"
			embedded := fstest.MapFS{"debug/SKILL.md": &fstest.MapFile{Data: content}}
			diskDir := ""
			want := "embedded winner"
			if disk {
				if source == "disk-flat" {
					want = "disk flat winner"
					embedded["debug.md"] = &fstest.MapFile{Data: skillFile("debug", "Disk flat", "", want)}
				}
				diskDir = writeSkillFiles(t, embedded)
				embedded = fstest.MapFS{"debug/SKILL.md": &fstest.MapFile{Data: skillFile("debug", "Embedded", "", "embedded winner")}}
			}
			eng := NewEngine()
			err := eng.Load(embedded, diskDir)
			if disk {
				if err != nil {
					t.Fatalf("Load invalid disk package overlay = %v", err)
				}
				out, err := eng.Render("debug", &SkillData{})
				if err != nil || out != want {
					t.Fatalf("invalid disk package lost fallback (%s): %q, %v, want %q", source, out, err, want)
				}
				if len(eng.Skills()) != 1 || eng.Get("different") != nil {
					t.Fatal("invalid disk package was exposed")
				}
				continue
			}
			if err == nil || !strings.Contains(err.Error(), "debug/SKILL.md") {
				t.Fatalf("Load invalid package (disk=%v) = %v", disk, err)
			}
			if len(eng.Skills()) != 0 || eng.Get("debug") != nil || eng.Get("different") != nil {
				t.Fatal("invalid package was exposed")
			}
		}
	}
}

func TestEngineDiskFragments(t *testing.T) {
	eng := NewEngine()
	embedded := fstest.MapFS{"debug/SKILL.md": &fstest.MapFile{Data: skillFile("debug", "Embedded", "", "embedded")}}
	diskDir := writeSkillFiles(t, fstest.MapFS{
		"debug.md":           &fstest.MapFile{Data: skillFile("debug", "Disk", "", `before {{template "gate" .}} after`)},
		"_fragments/gate.md": &fstest.MapFile{Data: []byte(`{{define "gate"}}disk fragment{{end}}`)},
	})
	if err := eng.Load(embedded, diskDir); err != nil {
		t.Fatal(err)
	}
	out, err := eng.Render("debug", &SkillData{})
	if err != nil || out != "before disk fragment after" {
		t.Fatalf("Render(debug) = %q, %v", out, err)
	}
	if got := eng.Get("gate"); got == nil || !got.IsFragment || len(eng.Skills()) != 1 {
		t.Fatalf("disk fragment metadata = %+v; public = %+v", got, eng.Skills())
	}
}

func TestEnginePackageMetadata(t *testing.T) {
	extra := `prompt: true
args:
  - name: task
    description: Requested task
related: [review]
tags: [coding]
metadata:
  task_classes: [task, code]
  effect_class: write-local
  refusal: allowed`
	content := skillFile("debug", "Debug a program", extra, "body")
	want := SkillMetadata{TaskClasses: []string{"task", "code"}, EffectClass: "write-local", Refusal: "allowed"}
	parsed, err := parseFrontmatter(content)
	if err != nil {
		t.Fatal(err)
	}
	eng := NewEngine()
	if err := eng.Load(fstest.MapFS{"debug/SKILL.md": &fstest.MapFile{Data: content}}, ""); err != nil {
		t.Fatal(err)
	}
	public := eng.Skills()
	if len(public) != 1 {
		t.Fatalf("Skills() = %+v", public)
	}
	for _, meta := range []*SkillMeta{parsed, eng.Get("debug"), &public[0]} {
		if meta == nil || !reflect.DeepEqual(meta.Metadata, want) {
			t.Fatalf("metadata not preserved: %+v", meta)
		}
		if meta.Prompt == nil || !*meta.Prompt || !reflect.DeepEqual(meta.Args, []ArgDef{{Name: "task", Description: "Requested task"}}) ||
			!reflect.DeepEqual(meta.Related, []string{"review"}) || !reflect.DeepEqual(meta.Tags, []string{"coding"}) {
			t.Fatalf("existing metadata not preserved: %+v", meta)
		}
	}
}

func TestEngineInvalidFlatDiskOverrideIsSkipped(t *testing.T) {
	embedded := fstest.MapFS{"debug/SKILL.md": &fstest.MapFile{Data: skillFile("debug", "Embedded", "", "embedded winner")}}
	diskDir := writeSkillFiles(t, fstest.MapFS{"debug.md": &fstest.MapFile{Data: []byte("legacy file without frontmatter")}})
	eng := NewEngine()
	if err := eng.Load(embedded, diskDir); err != nil {
		t.Fatal(err)
	}
	out, err := eng.Render("debug", &SkillData{})
	if err != nil || out != "embedded winner" {
		t.Fatalf("invalid legacy overlay replaced embedded package: %q, %v", out, err)
	}
}
