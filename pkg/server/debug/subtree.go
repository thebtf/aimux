package debug

import (
	"errors"
	"fmt"
	"strings"

	"github.com/thebtf/aimux/loom"
)

// ErrSubtreeCycle indicates corrupted tree data with a repeated task ID.
var ErrSubtreeCycle = errors.New("debug subtree: cycle detected")

// TreeGetter is the Loom subset needed by the internal subtree formatter.
type TreeGetter interface {
	GetTree(taskID string, maxDepth int) ([]loom.TaskNode, error)
}

// FormatSubtree renders a bounded Loom task subtree for internal debug output.
func FormatSubtree(tree TreeGetter, taskID string, maxDepth int) (string, error) {
	if tree == nil {
		return "", fmt.Errorf("debug subtree: tree getter is nil")
	}
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return "", fmt.Errorf("debug subtree: task_id is required")
	}

	nodes, err := tree.GetTree(taskID, maxDepth)
	if err != nil {
		return "", err
	}
	return formatSubtreeNodes(nodes)
}

func formatSubtreeNodes(nodes []loom.TaskNode) (string, error) {
	if len(nodes) == 0 {
		return "", fmt.Errorf("debug subtree: empty tree")
	}

	seen := make(map[string]struct{}, len(nodes))
	var b strings.Builder
	for _, node := range nodes {
		if strings.TrimSpace(node.ID) == "" {
			return "", fmt.Errorf("debug subtree: task node missing id")
		}
		if node.Depth < 0 {
			return "", fmt.Errorf("debug subtree: task %s has negative depth %d", node.ID, node.Depth)
		}
		if _, ok := seen[node.ID]; ok {
			return "", fmt.Errorf("%w: duplicate task_id %s", ErrSubtreeCycle, node.ID)
		}
		seen[node.ID] = struct{}{}

		parent := ""
		if node.ParentTaskID != "" {
			parent = " parent=" + node.ParentTaskID
		}
		fmt.Fprintf(
			&b,
			"%s- %s depth=%d worker=%s status=%s project=%s%s children=%d\n",
			strings.Repeat("  ", node.Depth),
			node.ID,
			node.Depth,
			node.WorkerType,
			node.Status,
			node.ProjectID,
			parent,
			len(node.SubtaskIDs),
		)
	}
	return b.String(), nil
}
