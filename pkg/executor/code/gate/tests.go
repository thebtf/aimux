package gate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

func testsCommand(projectType ProjectType) (commandSpec, bool) {
	switch projectType {
	case ProjectTypeGo:
		return commandSpec{name: "go", args: []string{"test", "./..."}}, true
	case ProjectTypeNode:
		return commandSpec{name: "npm", args: []string{"test"}}, true
	case ProjectTypePython:
		return commandSpec{name: "python", args: []string{"-m", "pytest"}}, true
	case ProjectTypeRust:
		return commandSpec{name: "cargo", args: []string{"test"}}, true
	default:
		return commandSpec{}, false
	}
}

// HasTests detects whether the project has an explicit test surface.
func HasTests(cwd string, projectType ProjectType) (bool, error) {
	switch projectType {
	case ProjectTypeGo:
		return hasFileWithSuffix(cwd, "_test.go")
	case ProjectTypeNode:
		content, err := os.ReadFile(filepath.Join(cwd, "package.json"))
		if err != nil {
			return false, err
		}
		var pkg struct {
			Scripts map[string]string `json:"scripts"`
		}
		if err := json.Unmarshal(content, &pkg); err != nil {
			return false, err
		}
		return strings.TrimSpace(pkg.Scripts["test"]) != "", nil
	case ProjectTypePython:
		return hasPythonTests(cwd)
	case ProjectTypeRust:
		return true, nil
	default:
		return false, nil
	}
}

func hasFileWithSuffix(root string, suffix string) (bool, error) {
	found := false
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path != root && entry.IsDir() && shouldSkipTestWalkDir(entry.Name()) {
			return filepath.SkipDir
		}
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), suffix) {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	return found, err
}

func hasPythonTests(root string) (bool, error) {
	found := false
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := entry.Name()
		if path != root && entry.IsDir() && shouldSkipTestWalkDir(name) {
			return filepath.SkipDir
		}
		if !entry.IsDir() && (name == "conftest.py" ||
			(strings.HasPrefix(name, "test_") && strings.HasSuffix(name, ".py")) ||
			strings.HasSuffix(name, "_test.py")) {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	return found, err
}

func shouldSkipTestWalkDir(name string) bool {
	switch name {
	case ".git", "node_modules", "vendor", "target", ".venv", "__pycache__":
		return true
	default:
		return false
	}
}
