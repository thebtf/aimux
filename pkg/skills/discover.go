package skills

import (
	"bufio"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// DiscoverCallerSkills scans cwd for skill/agent files from known AI CLI tools
// and returns a deduplicated, sorted list of discovered names.
//
// Scanned locations:
//   - .claude/skills/*.md  — Claude Code skills
//   - .claude/agents/*.md  — Claude Code agents
//   - .agents/skills/*.md  — Codex format skills
//   - AGENTS.md            — parsed for "### name" and "- **name**:" headings
//
// Errors (missing dirs, permission denied) are silently skipped.
func DiscoverCallerSkills(cwd string) []string {
	if cwd == "" {
		return nil
	}

	seen := make(map[string]struct{})

	// Directories to scan for *.md files.
	mdDirs := []string{
		filepath.Join(cwd, ".claude", "skills"),
		filepath.Join(cwd, ".claude", "agents"),
		filepath.Join(cwd, ".agents", "skills"),
	}
	for _, dir := range mdDirs {
		scanMDDir(dir, seen)
	}

	// Parse AGENTS.md for agent names.
	parseAgentsMD(filepath.Join(cwd, "AGENTS.md"), seen)

	if len(seen) == 0 {
		return nil
	}

	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// scanMDDir reads all *.md files in dir and adds their base names (without
// extension) to seen. Missing or unreadable directories are silently skipped.
func scanMDDir(dir string, seen map[string]struct{}) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.EqualFold(filepath.Ext(name), ".md") {
			seen[strings.TrimSuffix(name, filepath.Ext(name))] = struct{}{}
		}
	}
}

// parseAgentsMD reads an AGENTS.md file and extracts agent names from lines
// matching "### <name>" or "- **<name>**:". Missing file is silently skipped.
func parseAgentsMD(path string, seen map[string]struct{}) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Pattern 1: ### AgentName
		if strings.HasPrefix(line, "### ") {
			name := strings.TrimSpace(strings.TrimPrefix(line, "### "))
			if name != "" {
				seen[name] = struct{}{}
			}
			continue
		}

		// Pattern 2: - **AgentName**:
		if strings.HasPrefix(line, "- **") {
			rest := strings.TrimPrefix(line, "- **")
			if idx := strings.Index(rest, "**"); idx > 0 {
				name := strings.TrimSpace(rest[:idx])
				if name != "" {
					seen[name] = struct{}{}
				}
			}
		}
	}
}
