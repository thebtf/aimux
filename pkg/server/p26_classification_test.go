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

	"github.com/thebtf/aimux/pkg/think"
	"github.com/thebtf/aimux/pkg/think/patterns"
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

type p26RuntimeToolContract struct {
	Contracted     bool
	Classification string
	AdapterKind    string
}

func TestP26ClassificationCoverage(t *testing.T) {
	repoRoot := p26RepoRoot(t)
	serverPath := filepath.Join(repoRoot, "pkg", "server", "server.go")
	artifactPath := filepath.Join(repoRoot, "config", "p26", "classification.v1.json")

	liveTools, liveActions, liveContracts, err := p26ExtractRuntimeToolCoverage(serverPath)
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

	for _, tool := range p26SortedMapKeys(artifact.Tools) {
		spec := artifact.Tools[tool]
		contract := liveContracts[tool]
		if p26SpecRequiresAsyncContract(spec) {
			if !contract.Contracted {
				errs = append(errs, fmt.Sprintf("async_mandatory tool %q must register through registerContractedTool", tool))
				continue
			}
			if contract.Classification != p26AsyncMandatory {
				errs = append(errs, fmt.Sprintf("async_mandatory tool %q runtime contract classification = %q", tool, contract.Classification))
			}
			if strings.TrimSpace(contract.AdapterKind) == "" {
				errs = append(errs, fmt.Sprintf("async_mandatory tool %q runtime contract adapter_kind is empty", tool))
			}
		} else if contract.Contracted && contract.Classification == p26AsyncMandatory {
			errs = append(errs, fmt.Sprintf("tool %q runtime contract is async_mandatory but artifact is not", tool))
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

func p26SpecRequiresAsyncContract(spec p26ToolClassificationSpec) bool {
	if spec.Classification == p26AsyncMandatory {
		return true
	}
	for _, classification := range spec.Actions {
		if classification == p26AsyncMandatory {
			return true
		}
	}
	return false
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

func p26ExtractRuntimeToolCoverage(serverPath string) (map[string]struct{}, map[string]map[string]struct{}, map[string]p26RuntimeToolContract, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, serverPath, nil, parser.SkipObjectResolution)
	if err != nil {
		return nil, nil, nil, err
	}
	files := []*ast.File{file}
	serverDir := filepath.Dir(serverPath)
	for _, name := range []string{"think_harness.go", "task_tool.go", "patterns.go"} {
		path := filepath.Join(serverDir, name)
		if _, statErr := os.Stat(path); statErr != nil {
			continue
		}
		parsedFile, parseErr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if parseErr != nil {
			return nil, nil, nil, parseErr
		}
		files = append(files, parsedFile)
	}

	registerTools := p26FindFuncDecl(file, "registerTools")
	if registerTools == nil {
		return nil, nil, nil, fmt.Errorf("registerTools function not found in %s", serverPath)
	}

	tools := make(map[string]struct{})
	actionsByTool := make(map[string]map[string]struct{})
	contractsByTool := make(map[string]p26RuntimeToolContract)
	addTool := func(toolName string, actions map[string]struct{}, contract p26RuntimeToolContract) error {
		if _, exists := tools[toolName]; exists {
			return fmt.Errorf("duplicate tool registration for %q", toolName)
		}
		tools[toolName] = struct{}{}
		if len(actions) > 0 {
			actionsByTool[toolName] = actions
		}
		if contract.Contracted {
			contractsByTool[toolName] = contract
		}
		return nil
	}
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
			var (
				toolName string
				actions  map[string]struct{}
				contract p26RuntimeToolContract
				err      error
				matched  bool
			)
			switch {
			case p26IsSelectorCall(call.Fun, "AddTool"):
				toolName, actions, err = p26ParseAddToolCall(call, fset)
				matched = true
			case p26IsSelectorCall(call.Fun, "registerContractedTool"):
				toolName, actions, contract, err = p26ParseContractedToolCall(call, fset)
				matched = true
			default:
				return true
			}
			if err != nil {
				parseErr = err
				return false
			}
			if !matched {
				return true
			}
			if err := addTool(toolName, actions, contract); err != nil {
				parseErr = err
				return false
			}
			return true
		})
		return parseErr
	}

	if err := parseRegistration(registerTools); err != nil {
		return nil, nil, nil, err
	}

	for _, delegated := range []string{"registerThinkHarnessTool", "registerTaskTool"} {
		if !p26FuncContainsReceiverCall(registerTools, delegated) {
			return nil, nil, nil, fmt.Errorf("registerTools no longer delegates to %s; update P26 coverage extractor", delegated)
		}
		fn := p26FindFuncDeclInFiles(files, delegated)
		if fn == nil {
			return nil, nil, nil, fmt.Errorf("%s function not found for delegated P26 coverage", delegated)
		}
		if err := parseRegistration(fn); err != nil {
			return nil, nil, nil, err
		}
	}
	if !p26FuncContainsReceiverCall(registerTools, "registerPatternTools") {
		return nil, nil, nil, fmt.Errorf("registerTools no longer delegates to registerPatternTools; update P26 coverage extractor")
	}
	if err := p26AddCognitiveMoveTools(addTool); err != nil {
		return nil, nil, nil, err
	}
	if len(tools) == 0 {
		return nil, nil, nil, fmt.Errorf("no tools discovered in registerTools; unsupported registration shape")
	}
	return tools, actionsByTool, contractsByTool, nil
}

func p26AddCognitiveMoveTools(addTool func(string, map[string]struct{}, p26RuntimeToolContract) error) error {
	patterns.RegisterAll()
	count := 0
	for _, name := range think.GetAllPatterns() {
		if name == "think" {
			continue
		}
		if think.GetPattern(name) == nil {
			continue
		}
		if err := addTool(name, nil, p26RuntimeToolContract{}); err != nil {
			return err
		}
		count++
	}
	if count == 0 {
		return fmt.Errorf("no cognitive move tools discovered from think pattern registry")
	}
	return nil
}

func p26FuncContainsReceiverCall(fn *ast.FuncDecl, methodName string) bool {
	found := false
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if found {
			return false
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel == nil || sel.Sel.Name != methodName {
			return true
		}
		if ident, ok := sel.X.(*ast.Ident); ok && ident.Name == "s" {
			found = true
			return false
		}
		return true
	})
	return found
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
	return p26ParseNewToolCall(newToolCall, fset)
}

func p26ParseContractedToolCall(call *ast.CallExpr, fset *token.FileSet) (string, map[string]struct{}, p26RuntimeToolContract, error) {
	if len(call.Args) < 2 {
		return "", nil, p26RuntimeToolContract{}, p26UnsupportedCallErr(fset, call.Pos(), "registerContractedTool call requires contract and mcp.NewTool arguments")
	}

	contract, err := p26ParseToolContract(call.Args[0], fset)
	if err != nil {
		return "", nil, p26RuntimeToolContract{}, err
	}
	newToolCall, ok := call.Args[1].(*ast.CallExpr)
	if !ok || !p26IsSelectorCall(newToolCall.Fun, "NewTool") {
		return "", nil, p26RuntimeToolContract{}, p26UnsupportedCallErr(fset, call.Args[1].Pos(), "registerContractedTool second argument must be mcp.NewTool(...) literal")
	}
	toolName, actions, err := p26ParseNewToolCall(newToolCall, fset)
	if err != nil {
		return "", nil, p26RuntimeToolContract{}, err
	}
	if contract.Classification == "" {
		return "", nil, p26RuntimeToolContract{}, p26UnsupportedCallErr(fset, call.Args[0].Pos(), "toolContract Classification must be set")
	}
	if contract.Classification == p26AsyncMandatory && strings.TrimSpace(contract.AdapterKind) == "" {
		return "", nil, p26RuntimeToolContract{}, p26UnsupportedCallErr(fset, call.Args[0].Pos(), "async_mandatory toolContract AdapterKind must be set")
	}
	contract.Contracted = true
	contractName := strings.TrimSpace(p26ContractToolName(call.Args[0]))
	if contractName == "" {
		return "", nil, p26RuntimeToolContract{}, p26UnsupportedCallErr(fset, call.Args[0].Pos(), "toolContract Name must be set")
	}
	if contractName != toolName {
		return "", nil, p26RuntimeToolContract{}, p26UnsupportedCallErr(fset, call.Args[0].Pos(), fmt.Sprintf("toolContract Name %q does not match mcp.NewTool name %q", contractName, toolName))
	}
	return toolName, actions, contract, nil
}

func p26ParseToolContract(expr ast.Expr, fset *token.FileSet) (p26RuntimeToolContract, error) {
	lit, ok := expr.(*ast.CompositeLit)
	if !ok || !p26IsIdent(lit.Type, "toolContract") {
		return p26RuntimeToolContract{}, p26UnsupportedCallErr(fset, expr.Pos(), "first registerContractedTool argument must be toolContract{...}")
	}
	var contract p26RuntimeToolContract
	for _, element := range lit.Elts {
		kv, ok := element.(*ast.KeyValueExpr)
		if !ok {
			return p26RuntimeToolContract{}, p26UnsupportedCallErr(fset, element.Pos(), "toolContract must use keyed fields")
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok {
			continue
		}
		switch key.Name {
		case "Classification":
			value, ok := p26ContractString(kv.Value)
			if !ok {
				return p26RuntimeToolContract{}, p26UnsupportedCallErr(fset, kv.Value.Pos(), "toolContract Classification must be a literal or known constant")
			}
			contract.Classification = value
		case "AdapterKind":
			value, ok := p26StringLiteral(kv.Value)
			if !ok {
				return p26RuntimeToolContract{}, p26UnsupportedCallErr(fset, kv.Value.Pos(), "toolContract AdapterKind must be a string literal")
			}
			contract.AdapterKind = value
		}
	}
	return contract, nil
}

func p26ContractToolName(expr ast.Expr) string {
	lit, ok := expr.(*ast.CompositeLit)
	if !ok {
		return ""
	}
	for _, element := range lit.Elts {
		kv, ok := element.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok || key.Name != "Name" {
			continue
		}
		if value, ok := p26StringLiteral(kv.Value); ok {
			return value
		}
	}
	return ""
}

func p26ParseNewToolCall(newToolCall *ast.CallExpr, fset *token.FileSet) (string, map[string]struct{}, error) {
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

func p26ContractString(expr ast.Expr) (string, bool) {
	if value, ok := p26StringLiteral(expr); ok {
		return value, true
	}
	ident, ok := expr.(*ast.Ident)
	if !ok {
		return "", false
	}
	switch ident.Name {
	case "toolClassificationAsyncMandatory":
		return p26AsyncMandatory, true
	case "toolClassificationSyncOK":
		return p26SyncOK, true
	default:
		return "", false
	}
}

func p26IsIdent(expr ast.Expr, name string) bool {
	ident, ok := expr.(*ast.Ident)
	return ok && ident.Name == name
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
