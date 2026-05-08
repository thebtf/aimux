package gate

import (
	"os"
	"path/filepath"
	"strings"
)

// ProjectType is the detected build ecosystem.
type ProjectType string

const (
	ProjectTypeUnknown ProjectType = "unknown"
	ProjectTypeGo      ProjectType = "go"
	ProjectTypeNode    ProjectType = "node"
	ProjectTypePython  ProjectType = "python"
	ProjectTypeRust    ProjectType = "rust"
)

// DetectProjectType detects project type by marker files in deterministic order.
func DetectProjectType(cwd string) (ProjectType, error) {
	if strings.TrimSpace(cwd) == "" {
		return ProjectTypeUnknown, nil
	}
	markers := []struct {
		file        string
		projectType ProjectType
	}{
		{file: "go.mod", projectType: ProjectTypeGo},
		{file: "package.json", projectType: ProjectTypeNode},
		{file: "pyproject.toml", projectType: ProjectTypePython},
		{file: "Cargo.toml", projectType: ProjectTypeRust},
	}
	for _, marker := range markers {
		ok, err := fileExists(filepath.Join(cwd, marker.file))
		if err != nil {
			return ProjectTypeUnknown, err
		}
		if ok {
			return marker.projectType, nil
		}
	}
	return ProjectTypeUnknown, nil
}

func fileExists(path string) (bool, error) {
	info, err := os.Stat(path)
	if err == nil {
		return !info.IsDir(), nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}
