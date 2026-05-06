package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const (
	p26SyncOK         = "sync_ok"
	p26AsyncMandatory = "async_mandatory"
	p26Unknown        = "unknown"
)

type p26ClassificationArtifact struct {
	SchemaVersion string                               `json:"schema_version"`
	SourceOfTruth map[string]string                    `json:"source_of_truth"`
	Tools         map[string]p26ToolClassificationSpec `json:"tools"`
}

type p26ToolClassificationSpec struct {
	Classification string            `json:"classification,omitempty"`
	Actions        map[string]string `json:"actions,omitempty"`
}

func TestP26ClassificationCoverage(t *testing.T) {
	repoRoot := p26RepoRoot(t)
	serverPath := filepath.Join(repoRoot, "pkg", "server", "server.go")
	artifactPath := filepath.Join(repoRoot, "config", "p26", "classification.v1.json")

	liveTools, liveActions, err := p26ExtractRuntimeToolCoverage(serverPath)
	if err != nil {
		t.Fatalf("extract runtime registration coverage: %v", err)
	}

	artifact, err := p26LoadClassificationArtifact(artifactPath)
	if err != nil {
		t.Fatalf("load classification artifact: %v", err)
	}

	allowed := map[string]struct{}{
		p26SyncOK:         {},
		p26AsyncMandatory: {},
		p26Unknown:        {},
	}

	requiredActionTools := []string{"sessions", "think", "upgrade"}
	requiredActionSet := make(map[string]struct{}, len(requiredActionTools))
	for _, tool := range requiredActionTools {
		requiredActionSet[tool] = struct{}{}
	}

	var errs []string
	if artifact.SchemaVersion == "" {
		errs = append(errs, "artifact field schema_version must be non-empty")
	}
	if artifact.SourceOfTruth == nil || artifact.SourceOfTruth["runtime_registration"] == "" {
		errs = append(errs, "artifact source_of_truth.runtime_registration must be set")
	}
	if len(artifact.Tools) == 0 {
		errs = append(errs, "artifact tools map is empty")
	}

	for _, tool := range p26SortedSetKeys(liveTools) {
		if _, ok := artifact.Tools[tool]; !ok {
			errs = append(errs, fmt.Sprintf("missing tool entry: %q", tool))
		}
	}
	for _, tool := range p26SortedMapKeys(artifact.Tools) {
		if _, ok := liveTools[tool]; !ok {
			errs = append(errs, fmt.Sprintf("stale tool entry: %q", tool))
		}
	}

	for _, tool := range p26SortedMapKeys(artifact.Tools) {
		spec := artifact.Tools[tool]
		if strings.TrimSpace(spec.Classification) == "" && len(spec.Actions) == 0 {
			errs = append(errs, fmt.Sprintf("tool %q must define classification or actions", tool))
		}
		if spec.Classification != "" {
			if _, ok := allowed[spec.Classification]; !ok {
				errs = append(errs, fmt.Sprintf("invalid classification value for tool %q: %q", tool, spec.Classification))
			}
		}
		for _, action := range p26SortedMapKeys(spec.Actions) {
			v := spec.Actions[action]
			if _, ok := allowed[v]; !ok {
				errs = append(errs, fmt.Sprintf("invalid classification value for action %q on tool %q: %q", action, tool, v))
			}
		}
	}

	for _, tool := range p26SortedSetKeys(liveTools) {
		runtimeActions := liveActions[tool]
		spec, ok := artifact.Tools[tool]
		if !ok {
			continue
		}

		if len(runtimeActions) == 0 {
			if len(spec.Actions) > 0 {
				errs = append(errs, fmt.Sprintf("stale action entries for tool %q: %v", tool, p26SortedMapKeys(spec.Actions)))
			}
			continue
		}

		if len(spec.Actions) == 0 {
			errs = append(errs, fmt.Sprintf("missing action entries for tool %q", tool))
			continue
		}

		for _, action := range p26SortedSetKeys(runtimeActions) {
			if _, ok := spec.Actions[action]; !ok {
				errs = append(errs, fmt.Sprintf("missing action entry: tool=%q action=%q", tool, action))
			}
		}
		for _, action := range p26SortedMapKeys(spec.Actions) {
			if _, ok := runtimeActions[action]; !ok {
				errs = append(errs, fmt.Sprintf("stale action entry: tool=%q action=%q", tool, action))
			}
		}
	}

	for _, tool := range requiredActionTools {
		runtimeActions := liveActions[tool]
		if len(runtimeActions) == 0 {
			errs = append(errs, fmt.Sprintf("runtime action enum missing for required action tool %q", tool))
			continue
		}
		spec, ok := artifact.Tools[tool]
		if !ok {
			continue
		}
		if len(spec.Actions) == 0 {
			errs = append(errs, fmt.Sprintf("required action tool %q must use action-level classifications", tool))
		}
	}

	for _, tool := range p26SortedMapKeys(artifact.Tools) {
		spec := artifact.Tools[tool]
		if len(spec.Actions) > 0 {
			if _, ok := requiredActionSet[tool]; ok {
				continue
			}
			if len(liveActions[tool]) == 0 {
				errs = append(errs, fmt.Sprintf("stale action entries for non-action tool %q", tool))
			}
		}
	}

	if len(errs) > 0 {
		sort.Strings(errs)
		t.Fatalf("P26 classification coverage failed:\n- %s", strings.Join(errs, "\n- "))
	}
}

func p26RepoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", ".."))
}

func p26LoadClassificationArtifact(path string) (*p26ClassificationArtifact, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var artifact p26ClassificationArtifact
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&artifact); err != nil {
		return nil, err
	}
	if decoder.More() {
		return nil, fmt.Errorf("unexpected trailing JSON content in %s", path)
	}
	return &artifact, nil
}

func p26ExtractRuntimeToolCoverage(serverPath string) (map[string]struct{}, map[string]map[string]struct{}, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, serverPath, nil, parser.SkipObjectResolution)
	if err != nil {
		return nil, nil, err
	}
	files := []*ast.File{file}
	thinkHarnessPath := filepath.Join(filepath.Dir(serverPath), "think_harness.go")
	if _, statErr := os.Stat(thinkHarnessPath); statErr == nil {
		thinkHarnessFile, parseErr := parser.ParseFile(fset, thinkHarnessPath, nil, parser.SkipObjectResolution)
		if parseErr != nil {
			return nil, nil, parseErr
		}
		files = append(files, thinkHarnessFile)
	}

	registerTools := p26FindFuncDecl(file, "registerTools")
	if registerTools == nil {
		return nil, nil, fmt.Errorf("registerTools function not found in %s", serverPath)
	}
	thinkHarnessTools := p26FindFuncDeclInFiles(files, "registerThinkHarnessTool")

	tools := make(map[string]struct{})
	actionsByTool := make(map[string]map[string]struct{})
	parseRegistration := func(fn *ast.FuncDecl) error {
		var parseErr error
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			if parseErr != nil {
				return false
			}
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if !p26IsSelectorCall(call.Fun, "AddTool") {
				return true
			}

			toolName, actions, err := p26ParseAddToolCall(call, fset)
			if err != nil {
				parseErr = err
				return false
			}
			if _, exists := tools[toolName]; exists {
				parseErr = fmt.Errorf("duplicate tool registration for %q", toolName)
				return false
			}
			tools[toolName] = struct{}{}
			if len(actions) > 0 {
				actionsByTool[toolName] = actions
			}
			return true
		})
		return parseErr
	}

	if err := parseRegistration(registerTools); err != nil {
		return nil, nil, err
	}
	if thinkHarnessTools != nil {
		if err := parseRegistration(thinkHarnessTools); err != nil {
			return nil, nil, err
		}
	}
	if len(tools) == 0 {
		return nil, nil, fmt.Errorf("no tools discovered in registerTools; unsupported registration shape")
	}
	return tools, actionsByTool, nil
}

func p26FindFuncDecl(file *ast.File, name string) *ast.FuncDecl {
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		if fn.Name != nil && fn.Name.Name == name {
			return fn
		}
	}
	return nil
}

func p26FindFuncDeclInFiles(files []*ast.File, name string) *ast.FuncDecl {
	for _, file := range files {
		if fn := p26FindFuncDecl(file, name); fn != nil {
			return fn
		}
	}
	return nil
}

func p26ParseAddToolCall(addToolCall *ast.CallExpr, fset *token.FileSet) (string, map[string]struct{}, error) {
	if len(addToolCall.Args) == 0 {
		return "", nil, p26UnsupportedCallErr(fset, addToolCall.Pos(), "AddTool call has no arguments")
	}

	newToolCall, ok := addToolCall.Args[0].(*ast.CallExpr)
	if !ok || !p26IsSelectorCall(newToolCall.Fun, "NewTool") {
		return "", nil, p26UnsupportedCallErr(fset, addToolCall.Pos(), "AddTool first argument must be mcp.NewTool(...) literal")
	}
	if len(newToolCall.Args) == 0 {
		return "", nil, p26UnsupportedCallErr(fset, newToolCall.Pos(), "mcp.NewTool call has no arguments")
	}

	toolName, ok := p26StringLiteral(newToolCall.Args[0])
	if !ok {
		return "", nil, p26UnsupportedCallErr(fset, newToolCall.Args[0].Pos(), "mcp.NewTool first argument must be string literal tool name")
	}

	actions := make(map[string]struct{})
	for _, arg := range newToolCall.Args[1:] {
		withStringCall, ok := arg.(*ast.CallExpr)
		if !ok || !p26IsSelectorCall(withStringCall.Fun, "WithString") {
			continue
		}
		if len(withStringCall.Args) == 0 {
			continue
		}
		paramName, ok := p26StringLiteral(withStringCall.Args[0])
		if !ok || paramName != "action" {
			continue
		}

		hasEnum := false
		for _, wsArg := range withStringCall.Args[1:] {
			enumCall, ok := wsArg.(*ast.CallExpr)
			if !ok || !p26IsSelectorCall(enumCall.Fun, "Enum") {
				continue
			}
			hasEnum = true
			for _, enumArg := range enumCall.Args {
				actionName, ok := p26StringLiteral(enumArg)
				if !ok {
					return "", nil, p26UnsupportedCallErr(fset, enumArg.Pos(), "mcp.Enum action values must be string literals")
				}
				actions[actionName] = struct{}{}
			}
		}

		if !hasEnum {
			return "", nil, p26UnsupportedCallErr(fset, withStringCall.Pos(), "action parameter must declare mcp.Enum(...) literals")
		}
	}

	return toolName, actions, nil
}

func p26UnsupportedCallErr(fset *token.FileSet, pos token.Pos, detail string) error {
	p := fset.Position(pos)
	return fmt.Errorf("unsupported registerTools shape at %s: %s", p.String(), detail)
}

func p26IsSelectorCall(expr ast.Expr, selName string) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	return sel.Sel != nil && sel.Sel.Name == selName
}

func p26StringLiteral(expr ast.Expr) (string, bool) {
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	v, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", false
	}
	return v, true
}

func p26SortedSetKeys(m map[string]struct{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func p26SortedMapKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
