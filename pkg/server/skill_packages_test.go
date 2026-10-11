package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/thebtf/aimux/pkg/skills"
)

func TestSkillPackagePrompts(t *testing.T) {
	t.Setenv("AIMUX_MCP_THINKING_ONLY", "")
	builtins := map[string]skills.SkillMetadata{
		"developer":    {TaskClasses: []string{"code"}, EffectClass: "write-local", Refusal: "allowed"},
		"codereviewer": {TaskClasses: []string{"review"}, EffectClass: "read", Refusal: "allowed"},
		"docs":         {TaskClasses: []string{"task", "code"}, EffectClass: "write-local", Refusal: "allowed"},
		"pm":           {TaskClasses: []string{"task"}, EffectClass: "write-local", Refusal: "allowed"},
	}
	for name := range builtins {
		for _, relative := range []string{"SKILL.md", "evals/trigger-eval.json"} {
			if data, err := fs.ReadFile(skillsEmbedFS, name+"/"+relative); err != nil || len(data) == 0 {
				t.Fatalf("embedded %s/%s is missing or empty: %v", name, relative, err)
			}
		}
	}

	for _, source := range []string{"embedded", "disk-packages"} {
		t.Run(source, func(t *testing.T) {
			srv := testServer(t)
			if source == "disk-packages" {
				dir := t.TempDir()
				if err := fs.WalkDir(skillsEmbedFS, ".", func(name string, entry fs.DirEntry, err error) error {
					if err != nil || entry.IsDir() {
						return err
					}
					data, err := fs.ReadFile(skillsEmbedFS, name)
					if err != nil {
						return err
					}
					filePath := filepath.Join(dir, filepath.FromSlash(name))
					if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
						return err
					}
					return os.WriteFile(filePath, data, 0o644)
				}); err != nil {
					t.Fatal(err)
				}
				srv.skillEngine = skills.NewEngine()
				if err := srv.skillEngine.Load(fstest.MapFS{}, dir); err != nil {
					t.Fatal(err)
				}
			}
			if srv.skillEngine == nil || len(srv.skillEngine.Skills()) != len(builtins) {
				t.Fatal("four built-in package skills were not loaded")
			}

			// Isolate skill registration from the four preserved hand-coded prompts.
			srv.mcp = mcpserver.NewMCPServer("skill-package-test", "1", mcpserver.WithPromptCapabilities(true))
			srv.registerSkillPrompts()
			var listed mcp.ListPromptsResult
			if err := json.Unmarshal(skillPromptRPC(t, srv, "prompts/list", map[string]any{}), &listed); err != nil {
				t.Fatal(err)
			}
			if len(listed.Prompts) != len(builtins) {
				t.Fatalf("catalog has %d prompts, want exactly four: %+v", len(listed.Prompts), listed.Prompts)
			}
			seen := make(map[string]bool)
			for _, prompt := range listed.Prompts {
				wantMetadata, expected := builtins[prompt.Name]
				if !expected || seen[prompt.Name] {
					t.Fatalf("unexpected, support-file, or duplicate prompt %q", prompt.Name)
				}
				seen[prompt.Name] = true
				meta := srv.skillEngine.Get(prompt.Name)
				if meta == nil || meta.FilePath != prompt.Name+"/SKILL.md" || !reflect.DeepEqual(meta.Metadata, wantMetadata) {
					t.Fatalf("package metadata for %q = %+v", prompt.Name, meta)
				}
				if len(prompt.Arguments) == 0 || len(prompt.Arguments) != len(meta.Args) || prompt.Description != meta.Description {
					t.Fatalf("prompt declaration lost metadata: %+v", prompt)
				}
				args := make(map[string]string)
				for i, argument := range prompt.Arguments {
					if argument.Name != meta.Args[i].Name || argument.Description != meta.Args[i].Description {
						t.Fatalf("argument metadata changed: %+v, want %+v", argument, meta.Args[i])
					}
					args[argument.Name] = fmt.Sprintf("argument-marker-%s-%s", prompt.Name, argument.Name)
					if argument.Name == "as_gate" {
						args[argument.Name] = "true"
					}
				}
				text := skillPromptRenderedText(t, srv, prompt.Name, args)
				for name, value := range args {
					if !strings.Contains(text, value) {
						t.Errorf("%s did not render %s=%q", prompt.Name, name, value)
					}
					args[name] = value + "-changed"
					if name == "as_gate" {
						args[name] = "false"
					}
					changed := skillPromptRenderedText(t, srv, prompt.Name, args)
					if changed == text || !strings.Contains(changed, args[name]) {
						t.Errorf("%s does not bind argument %s to rendered output", prompt.Name, name)
					}
					args[name] = value
				}
			}
		})
	}
}

func skillPromptRPC(t *testing.T, srv *Server, method string, params any) json.RawMessage {
	t.Helper()
	request, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	if err != nil {
		t.Fatal(err)
	}
	response := srv.mcp.HandleMessage(context.Background(), request)
	raw, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	var rpc struct {
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(raw, &rpc); err != nil {
		t.Fatal(err)
	}
	if len(rpc.Error) != 0 || len(rpc.Result) == 0 {
		t.Fatalf("%s failed: %s", method, raw)
	}
	return rpc.Result
}

func skillPromptRenderedText(t *testing.T, srv *Server, name string, args map[string]string) string {
	t.Helper()
	var result struct {
		Messages []struct {
			Content struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(skillPromptRPC(t, srv, "prompts/get", map[string]any{"name": name, "arguments": args}), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Messages) != 1 || result.Messages[0].Content.Type != "text" || result.Messages[0].Content.Text == "" {
		t.Fatalf("%s returned no rendered text: %+v", name, result)
	}
	return result.Messages[0].Content.Text
}
