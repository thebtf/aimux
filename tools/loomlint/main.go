// loomlint walks a target directory and fails on any import outside the allow-list.
// It uses go/parser + go/ast to extract imports — never regex.
//
// Allow-list matching:
//   - An import is allowed if it has no '/' before the first '.', which is the
//     stdlib convention (e.g. "fmt", "os", "sync").
//   - An import is allowed if it starts with any of the configured allow-prefixes.
//
// Usage:
//
//	go run ./tools/loomlint [--allow <prefix>]... <dir>
package main

import (
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// defaultAllowPrefixes is the Phase 0 allow-list for pkg/loom core files.
// It covers: stdlib (matched separately via isStdlib), google/uuid,
// otel metric interface, and the loom sub-packages themselves.
var defaultAllowPrefixes = []string{
	"github.com/google/uuid",
	"go.opentelemetry.io/otel/metric",
	"github.com/thebtf/aimux/pkg/loom",
}

// Violation records a single forbidden import found during the walk.
type Violation struct {
	Path string // import path
	File string // absolute file path
	Line int    // line number in the file
}

func (v Violation) String() string {
	return fmt.Sprintf("forbidden import: %s in %s:%d", v.Path, v.File, v.Line)
}

// isStdlib reports whether an import path is a Go standard library package.
// Stdlib packages have no dots before the first slash component (e.g. "fmt",
// "os/exec", "encoding/json"). Third-party modules always start with a
// domain-like component containing a dot (e.g. "github.com/...", "golang.org/...").
func isStdlib(importPath string) bool {
	first := strings.SplitN(importPath, "/", 2)[0]
	return !strings.Contains(first, ".")
}

// isAllowed reports whether importPath is permitted under allowPrefixes.
// Stdlib imports are always allowed regardless of allowPrefixes.
func isAllowed(importPath string, allowPrefixes []string) bool {
	if isStdlib(importPath) {
		return true
	}
	for _, prefix := range allowPrefixes {
		if importPath == prefix || strings.HasPrefix(importPath, prefix+"/") {
			return true
		}
	}
	return false
}

// lintFile parses a single Go source file and returns violations.
// It uses ast.Inspect to walk the AST and extract all import declarations.
func lintFile(filePath string, fset *token.FileSet, allowPrefixes []string) ([]Violation, error) {
	f, err := parser.ParseFile(fset, filePath, nil, parser.ImportsOnly)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", filePath, err)
	}

	var violations []Violation
	ast.Inspect(f, func(n ast.Node) bool {
		imp, ok := n.(*ast.ImportSpec)
		if !ok {
			return true
		}
		importPath, unquoteErr := strconv.Unquote(imp.Path.Value)
		if unquoteErr != nil {
			// Malformed import literal — treat as a violation with a descriptive path.
			importPath = imp.Path.Value
		}
		if !isAllowed(importPath, allowPrefixes) {
			pos := fset.Position(imp.Path.Pos())
			violations = append(violations, Violation{
				Path: importPath,
				File: filePath,
				Line: pos.Line,
			})
		}
		return true
	})
	return violations, nil
}

// Lint walks target recursively, parsing every .go file, and returns an error
// listing all forbidden imports. Returns nil when the directory is clean.
// allowPrefixes is the set of non-stdlib import prefixes that are permitted.
func Lint(target string, allowPrefixes []string) error {
	fset := token.NewFileSet()
	var violations []Violation

	err := filepath.Walk(target, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}

		fileViolations, err := lintFile(path, fset, allowPrefixes)
		if err != nil {
			return err
		}
		violations = append(violations, fileViolations...)
		return nil
	})
	if err != nil {
		return fmt.Errorf("loomlint: walk %s: %w", target, err)
	}

	if len(violations) == 0 {
		return nil
	}

	var sb strings.Builder
	for i, v := range violations {
		if i > 0 {
			sb.WriteByte('\n')
		}
		sb.WriteString(v.String())
	}
	return fmt.Errorf("%s", sb.String())
}

// repeatable implements flag.Value for a string slice flag that can be repeated.
type repeatable []string

func (r *repeatable) String() string {
	return strings.Join(*r, ",")
}

func (r *repeatable) Set(v string) error {
	*r = append(*r, v)
	return nil
}

func main() {
	var allowFlags repeatable
	for _, d := range defaultAllowPrefixes {
		allowFlags = append(allowFlags, d)
	}

	// Re-define the flag after seeding defaults so printed usage shows defaults.
	flag.Var(&allowFlags, "allow", "allow import prefix (repeatable; defaults to Phase 0 set)")
	flag.Parse()

	if flag.NArg() != 1 {
		fmt.Fprintf(os.Stderr, "usage: loomlint [--allow <prefix>]... <dir>\n")
		os.Exit(2)
	}

	target := flag.Arg(0)
	if err := Lint(target, allowFlags); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
