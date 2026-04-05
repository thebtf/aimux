package orchestrator

import (
	"strings"
)

// DiffHunk represents a single hunk from a unified diff.
// Delimited by @@ markers in git unified diff format.
type DiffHunk struct {
	Index    int    `json:"index"`
	Header   string `json:"header"`   // @@ -a,b +c,d @@ context
	Content  string `json:"content"`  // full hunk text including header
	FilePath string `json:"file_path"`
	AddLines int    `json:"add_lines"`
	DelLines int    `json:"del_lines"`
}

// DiffFile represents all hunks for a single file.
type DiffFile struct {
	Path  string     `json:"path"`
	Hunks []DiffHunk `json:"hunks"`
}

// ParseUnifiedDiff parses a unified diff string into files and hunks.
func ParseUnifiedDiff(diff string) []DiffFile {
	var files []DiffFile
	var currentFile *DiffFile
	var currentHunk *DiffHunk
	hunkIndex := 0

	lines := strings.Split(diff, "\n")

	for _, line := range lines {
		// New file header: --- a/path or +++ b/path
		if strings.HasPrefix(line, "+++ b/") {
			path := strings.TrimPrefix(line, "+++ b/")
			if currentFile != nil && currentHunk != nil {
				currentFile.Hunks = append(currentFile.Hunks, *currentHunk)
				currentHunk = nil
			}
			if currentFile != nil {
				files = append(files, *currentFile)
			}
			currentFile = &DiffFile{Path: path}
			hunkIndex = 0
			continue
		}

		if strings.HasPrefix(line, "--- ") {
			continue // skip --- line
		}

		if strings.HasPrefix(line, "diff --git") {
			continue // skip diff header
		}

		if strings.HasPrefix(line, "index ") {
			continue // skip index line
		}

		// Hunk header: @@ -a,b +c,d @@ optional context
		if strings.HasPrefix(line, "@@") {
			if currentHunk != nil && currentFile != nil {
				currentFile.Hunks = append(currentFile.Hunks, *currentHunk)
			}
			filePath := ""
			if currentFile != nil {
				filePath = currentFile.Path
			}
			currentHunk = &DiffHunk{
				Index:    hunkIndex,
				Header:   line,
				Content:  line + "\n",
				FilePath: filePath,
			}
			hunkIndex++
			continue
		}

		// Hunk content
		if currentHunk != nil {
			currentHunk.Content += line + "\n"
			if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
				currentHunk.AddLines++
			}
			if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") {
				currentHunk.DelLines++
			}
		}
	}

	// Flush last hunk and file
	if currentHunk != nil && currentFile != nil {
		currentFile.Hunks = append(currentFile.Hunks, *currentHunk)
	}
	if currentFile != nil {
		files = append(files, *currentFile)
	}

	return files
}

// AllHunks flattens all hunks from all files into a single slice.
func AllHunks(files []DiffFile) []DiffHunk {
	var hunks []DiffHunk
	for _, f := range files {
		hunks = append(hunks, f.Hunks...)
	}
	return hunks
}

// ReassembleDiff creates a unified diff string from approved/modified hunks.
func ReassembleDiff(files []DiffFile, approved map[int]string) string {
	var sb strings.Builder

	for _, f := range files {
		hasApproved := false
		for _, h := range f.Hunks {
			if _, ok := approved[h.Index]; ok {
				hasApproved = true
				break
			}
		}
		if !hasApproved {
			continue
		}

		sb.WriteString("--- a/" + f.Path + "\n")
		sb.WriteString("+++ b/" + f.Path + "\n")

		for _, h := range f.Hunks {
			if content, ok := approved[h.Index]; ok {
				sb.WriteString(content)
			}
		}
	}

	return sb.String()
}
