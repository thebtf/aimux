// loomlint walks a target directory and fails on any import outside the allow-list.
// It uses go/parser + go/ast to extract imports — never regex.
//
// Allow-list matching:
//   - An import is allowed if it has no '/' before the first '.', which is the
//     stdlib convention (e.g. "fmt", "os", "sync").
//   - An import is allowed if it starts with any of the configured allow-prefixes.
//
// Scope filtering honors spec NFR-1 "core (non-test non-workers code)":
//   - Test files (*_test.go) are skipped by default (toggle via --include-tests)
//   - Directories matching --skip-dir patterns are skipped (repeatable; defaults
//     include "workers" so the subpackage of aimux-specific adapters is excluded)
//
// Usage:
//
//	go run ./tools/loomlint [--allow <prefix>]... [--skip-dir <name>]... [--include-tests] <dir>
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
	"github.com/thebtf/aimux/loom",
}

// defaultSkipDirs is the Phase 0 set of sub-directories excluded from boundary
// enforcement. `workers` holds the aimux-specific concrete Worker adapters which
// by design import aimux/pkg/* and are out of scope for the core closure.
var defaultSkipDirs = []string{"workers"}

// LintOptions controls how Lint walks the target and classifies imports.
type LintOptions struct {
	// AllowPrefixes is the set of non-stdlib import prefixes that are permitted.
	AllowPrefixes []string

	// IncludeTests, when true, includes *_test.go files in the walk. Default (false)
	// means test files are skipped — boundary enforcement applies to production code only.
	IncludeTests bool

	// SkipDirs lists directory names (not paths) that terminate the walk. The walker
	// compares only the base name of each directory it enters. Matches are case-sensitive.
	SkipDirs []string
}

// Violation records a single forbidden import found during the walk.
type Violation struct {
	Path string // import path
	File string // file path as visited by walker (absolute or relative, matching the target argument)
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

// skipDirName reports whether base matches any entry in skipDirs.
func skipDirName(base string, skipDirs []string) bool {
	for _, s := range skipDirs {
		if base == s {
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

// Lint walks target recursively, parsing every production .go file, and returns
// an error listing all forbidden imports. Returns nil when the directory is clean.
// Files under any directory matching opts.SkipDirs are excluded. Test files are
// excluded unless opts.IncludeTests is true.
func Lint(target string, opts LintOptions) error {
	target = filepath.Clean(target) // normalise before comparison in walker
	fset := token.NewFileSet()
	var violations []Violation

	err := filepath.WalkDir(target, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			// Skip on directory name match. Do not compare the root target itself
			// against the skip list — the user explicitly pointed at it.
			if path != target && skipDirName(d.Name(), opts.SkipDirs) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		if !opts.IncludeTests && strings.HasSuffix(path, "_test.go") {
			return nil
		}

		fileViolations, err := lintFile(path, fset, opts.AllowPrefixes)
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

	var skipDirFlags repeatable
	for _, d := range defaultSkipDirs {
		skipDirFlags = append(skipDirFlags, d)
	}

	var includeTests bool

	flag.Var(&allowFlags, "allow", "allow import prefix (repeatable; defaults to Phase 0 set)")
	flag.Var(&skipDirFlags, "skip-dir", "skip directory name during walk (repeatable; default: workers)")
	flag.BoolVar(&includeTests, "include-tests", false, "include *_test.go files in the walk (default false — boundary enforcement is production-only)")
	flag.Parse()

	if flag.NArg() != 1 {
		fmt.Fprintf(os.Stderr, "usage: loomlint [--allow <prefix>]... [--skip-dir <name>]... [--include-tests] <dir>\n")
		os.Exit(2)
	}

	target := flag.Arg(0)
	opts := LintOptions{
		AllowPrefixes: allowFlags,
		IncludeTests:  includeTests,
		SkipDirs:      skipDirFlags,
	}
	if err := Lint(target, opts); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
