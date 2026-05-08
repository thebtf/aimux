package loom

import (
	"errors"
	"fmt"
	"time"
)

// ErrSubtaskDepthExceeded indicates a sub-task tree is deeper than the caller's
// requested traversal bound.
var ErrSubtaskDepthExceeded = errors.New("loom: subtask depth exceeded")

// TaskNode is the read model returned by GetTree.
type TaskNode struct {
	ID           string     `json:"id"`
	ParentTaskID string     `json:"parent_task_id,omitempty"`
	ProjectID    string     `json:"project_id"`
	WorkerType   WorkerType `json:"worker_type"`
	Status       TaskStatus `json:"status"`
	Depth        int        `json:"depth"`
	CreatedAt    time.Time  `json:"created_at"`
	StartedAt    *time.Time `json:"started_at,omitempty"`
	CompletedAt  *time.Time `json:"completed_at,omitempty"`
	SubtaskIDs   []string   `json:"subtask_ids,omitempty"`
}

// GetTree returns the pre-order sub-task tree rooted at rootID.
func (l *LoomEngine) GetTree(rootID string, maxDepth int) ([]TaskNode, error) {
	if maxDepth < 0 {
		return nil, ErrSubtaskDepthExceeded
	}
	root, err := l.store.Get(rootID)
	if err != nil {
		return nil, fmt.Errorf("loom: get tree root: %w", err)
	}
	nodes := make([]TaskNode, 0, 1)
	if err := l.appendTree(&nodes, root, 0, maxDepth); err != nil {
		return nil, err
	}
	return nodes, nil
}

func (l *LoomEngine) appendTree(nodes *[]TaskNode, task *Task, depth, maxDepth int) error {
	if depth > maxDepth {
		return ErrSubtaskDepthExceeded
	}

	children, err := l.store.ListChildren(task.ID)
	if err != nil {
		return err
	}
	childIDs := make([]string, 0, len(children))
	for _, child := range children {
		childIDs = append(childIDs, child.ID)
	}

	*nodes = append(*nodes, TaskNode{
		ID:           task.ID,
		ParentTaskID: task.ParentTaskID,
		ProjectID:    task.ProjectID,
		WorkerType:   task.WorkerType,
		Status:       task.Status,
		Depth:        depth,
		CreatedAt:    task.CreatedAt,
		StartedAt:    task.DispatchedAt,
		CompletedAt:  task.CompletedAt,
		SubtaskIDs:   childIDs,
	})

	for _, child := range children {
		if err := l.appendTree(nodes, child, depth+1, maxDepth); err != nil {
			return err
		}
	}
	return nil
}
