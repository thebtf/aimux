package orchestrator

// FileAssignment maps files to agents for parallel execution.
// Each agent gets a distinct set of files — no two agents modify the same file.
type FileAssignment struct {
	AgentID string   `json:"agent_id"`
	Files   []string `json:"files"`
	Role    string   `json:"role"`
}

// AssignFiles splits a list of files across N agents.
// Files are distributed round-robin by default.
func AssignFiles(files []string, agentCount int) []FileAssignment {
	if agentCount <= 0 {
		agentCount = 1
	}

	assignments := make([]FileAssignment, agentCount)
	for i := range assignments {
		assignments[i] = FileAssignment{
			AgentID: generateAgentID(i),
			Files:   make([]string, 0),
		}
	}

	for i, file := range files {
		idx := i % agentCount
		assignments[idx].Files = append(assignments[idx].Files, file)
	}

	return assignments
}

func generateAgentID(index int) string {
	return string(rune('A'+index)) + "-agent"
}
