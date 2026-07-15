package swarm_test

// Source guard for CR-003 T005 [RED]: "no provider-specific type enters
// shared packages" (AIMUX-26 Task Group 2 AC). The provider-neutral live
// session binding seam lives in pkg/types and pkg/swarm; provider-native
// Codex/Grok/Antigravity resume/fork implementations are explicitly deferred
// to CR-004/CR-005/CR-005A and MUST live in their own packages, never as an
// import or a declared type/function name inside this shared seam.
//
// Unlike the other CR-003 T005 oracles in this package, this guard does not
// depend on the not-yet-implemented AcquireSessionBinding API: it inspects
// current, real, non-test source files and is expected to PASS today. Its
// purpose is to fail closed the moment CR-004/CR-005 (or any future change)
// leaks a provider-specific identifier into pkg/types or pkg/swarm
// production source. The synthetic-mutation test below proves the guard
// actually has teeth, mirroring the existing
// TestInspectSuppliedEvidenceSourceHasNoDiscoveryOrExecutionCalls pattern in
// swarm_internal_test.go.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// forbiddenProviderSubstrings are provider-specific identifiers that must
// never appear as an import-path segment or a declared type/function name in
// the shared, provider-neutral pkg/types/pkg/swarm session-binding seam.
// Matching is case-insensitive and substring-based so both "codex" and
// "CodexSessionBinding" are caught.
var forbiddenProviderSubstrings = []string{
	"codex", "grok", "antigravity", "openai", "anthropic",
}

// sessionBindingSeamSubstrings identify declarations belonging to the shared
// session-binding seam. Provider-neutrality is intentionally scoped to files
// with one of these declarations so unrelated provider-aware shared code is
// not rejected.
var sessionBindingSeamSubstrings = []string{
	"sessionbinding", "livesessionbinding", "sessionforker", "acquiresessionbinding",
}

// TestSessionSeamSourceRejectsProviderSpecificIdentifiers walks every
// non-test .go file in pkg/swarm and pkg/types. It first finds files that
// declare part of the session-binding seam, then rejects provider-specific
// imports and provider-specific names only within those seam declarations.
func TestSessionSeamSourceRejectsProviderSpecificIdentifiers(t *testing.T) {
	t.Parallel()

	for _, dir := range []string{".", filepath.Join("..", "types")} {
		dir := dir
		t.Run(dir, func(t *testing.T) {
			if err := inspectSessionSeamProviderNeutrality(dir); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// TestSessionSeamSourceGuardDetectsForbiddenIdentifier proves the guard
// rejects a provider-specific import in a seam file and provider-specific
// type/function names in seam declarations, while ignoring provider-aware
// declarations that are unrelated to the session-binding seam.
func TestSessionSeamSourceGuardDetectsForbiddenIdentifier(t *testing.T) {
	t.Parallel()

	const forbiddenImport = `package swarm

import _ "github.com/thebtf/aimux/pkg/providers/grok"

type SessionBindingRequest struct{}
`
	if err := inspectSourceProviderNeutrality("forbidden_import.go", []byte(forbiddenImport)); err == nil {
		t.Fatal("guard accepted a provider-specific import in a session seam file")
	}

	const forbiddenType = `package swarm

type CodexSessionBinding struct{}
`
	if err := inspectSourceProviderNeutrality("forbidden_type.go", []byte(forbiddenType)); err == nil {
		t.Fatal("guard accepted a provider-specific type name in a session seam declaration")
	}

	const forbiddenFunc = `package swarm

func acquireGrokSessionBinding() {}
`
	if err := inspectSourceProviderNeutrality("forbidden_func.go", []byte(forbiddenFunc)); err == nil {
		t.Fatal("guard accepted a provider-specific function name in a session seam declaration")
	}

	const neutralSource = `package swarm

import "context"

type LiveSessionBinding struct{}

func acquireSessionBinding(ctx context.Context) {}
`
	if err := inspectSourceProviderNeutrality("neutral.go", []byte(neutralSource)); err != nil {
		t.Fatalf("guard rejected genuinely neutral source: %v", err)
	}

	const nonSeamProviderAwareSource = `package swarm

import _ "github.com/thebtf/aimux/pkg/providers/codex"

type CodexExecutor struct{}

func acquireCodexExecutor() {}
`
	if err := inspectSourceProviderNeutrality("non_seam_provider_aware.go", []byte(nonSeamProviderAwareSource)); err != nil {
		t.Fatalf("guard rejected provider-aware source outside the session-binding seam: %v", err)
	}
}

// inspectSessionSeamProviderNeutrality inspects every non-test .go file
// directly inside dir (no recursion -- pkg/types and pkg/swarm are both flat
// packages). Files without a session-binding seam declaration are ignored.
func inspectSessionSeamProviderNeutrality(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		source, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return err
		}
		if err := inspectSourceProviderNeutrality(name, source); err != nil {
			return fmt.Errorf("%s: %w", filepath.Join(dir, name), err)
		}
	}
	return nil
}

// inspectSourceProviderNeutrality parses source. A source file is inspected
// only when it declares a session-binding seam type or function. In such a
// file, every import and only the selected seam declaration ASTs are checked.
func inspectSourceProviderNeutrality(filename string, source []byte) error {
	parsed, err := parser.ParseFile(token.NewFileSet(), filename, source, 0)
	if err != nil {
		return err
	}

	seamDeclarations := sessionBindingSeamDeclarations(parsed)
	if len(seamDeclarations) == 0 {
		return nil
	}

	for _, imp := range parsed.Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		if containsForbiddenProviderSubstring(path) {
			return fmt.Errorf("forbidden provider-specific import %q", path)
		}
	}

	for _, declaration := range seamDeclarations {
		if forbidden := forbiddenIdentifierInDeclaration(declaration); forbidden != "" {
			return fmt.Errorf("forbidden provider-specific identifier %q", forbidden)
		}
	}
	return nil
}

// sessionBindingSeamDeclarations returns only the type/function declaration
// ASTs that belong to the session-binding seam.
func sessionBindingSeamDeclarations(file *ast.File) []ast.Node {
	var declarations []ast.Node
	for _, declaration := range file.Decls {
		switch declaration := declaration.(type) {
		case *ast.GenDecl:
			if declaration.Tok != token.TYPE {
				continue
			}
			for _, spec := range declaration.Specs {
				typeSpec, ok := spec.(*ast.TypeSpec)
				if !ok || !isSessionBindingSeamDeclaration(typeSpec.Name.Name) {
					continue
				}
				declarations = append(declarations, typeSpec)
			}
		case *ast.FuncDecl:
			if isSessionBindingSeamDeclaration(declaration.Name.Name) {
				declarations = append(declarations, declaration)
			}
		}
	}
	return declarations
}

func forbiddenIdentifierInDeclaration(declaration ast.Node) string {
	var forbidden string
	ast.Inspect(declaration, func(node ast.Node) bool {
		if forbidden != "" {
			return false
		}
		if identifier, ok := node.(*ast.Ident); ok && containsForbiddenProviderSubstring(identifier.Name) {
			forbidden = identifier.Name
			return false
		}
		return true
	})
	return forbidden
}

func isSessionBindingSeamDeclaration(name string) bool {
	lower := strings.ToLower(name)
	for _, seam := range sessionBindingSeamSubstrings {
		if strings.Contains(lower, seam) {
			return true
		}
	}
	return false
}

func containsForbiddenProviderSubstring(s string) bool {
	lower := strings.ToLower(s)
	for _, forbidden := range forbiddenProviderSubstrings {
		if strings.Contains(lower, forbidden) {
			return true
		}
	}
	return false
}
