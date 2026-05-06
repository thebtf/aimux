package harness

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"
)

type StartRequest struct {
	Task           string `json:"task"`
	Goal           string `json:"goal,omitempty"`
	ContextSummary string `json:"context_summary"`
	SuccessSignal  string `json:"success_signal,omitempty"`
}

type StartResponse struct {
	SessionID         string               `json:"session_id"`
	Phase             Phase                `json:"phase"`
	AllowedMoveGroups []MoveGroup          `json:"allowed_move_groups"`
	RecommendedMoves  []MoveRecommendation `json:"recommended_moves"`
	MissingInputs     []string             `json:"missing_inputs"`
	NextPrompt        string               `json:"next_prompt"`
	KnowledgeState    KnowledgeLedger      `json:"knowledge_state"`
}

type Controller struct {
	store       Store
	catalog     MoveCatalog
	idGenerator func() string
}

type ControllerOption func(*Controller)

func WithIDGenerator(fn func() string) ControllerOption {
	return func(c *Controller) {
		if fn != nil {
			c.idGenerator = fn
		}
	}
}

func NewController(store Store, opts ...ControllerOption) *Controller {
	if store == nil {
		store = NewInMemoryStore()
	}
	c := &Controller{
		store:       store,
		catalog:     NewDefaultMoveCatalog(),
		idGenerator: defaultSessionID,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

func (c *Controller) Start(ctx context.Context, req StartRequest) (StartResponse, error) {
	if req.Task == "" {
		return StartResponse{}, invalidInputError("start requires task", "Provide the task the caller wants to reason about.")
	}
	if req.ContextSummary == "" {
		return StartResponse{}, invalidInputError("start requires context_summary", "Summarize the visible context for this thinking run.")
	}

	goal := req.Goal
	if goal == "" {
		goal = "Produce a supported caller-owned answer without premature finalization."
	}
	success := req.SuccessSignal
	if success == "" {
		success = "The caller can finalize with visible evidence, calibrated confidence, and no unresolved critical objections."
	}
	frame, err := NewTaskFrame(TaskFrame{
		Task:           req.Task,
		Goal:           goal,
		ContextSummary: req.ContextSummary,
		SuccessSignal:  success,
	})
	if err != nil {
		return StartResponse{}, err
	}

	session := NewThinkingSession(c.idGenerator(), frame)
	session.Ledger = KnowledgeLedger{
		Known: []LedgerEntry{{
			ID:     "context_summary",
			Text:   req.ContextSummary,
			Source: "caller",
			Status: "visible",
		}},
		Unknown: []LedgerEntry{{
			ID:     "work_product",
			Text:   "No caller work product has been observed yet.",
			Status: "open",
		}},
		Checkable: []LedgerEntry{{
			ID:     "finalization_support",
			Text:   "Finalization must be checked against evidence, gates, objections, and budget.",
			Status: "pending",
		}},
	}

	created, err := c.store.Create(ctx, session)
	if err != nil {
		return StartResponse{}, err
	}

	return StartResponse{
		SessionID:         created.ID,
		Phase:             created.Phase,
		AllowedMoveGroups: c.catalog.AllowedGroups(created.Phase),
		RecommendedMoves:  c.catalog.Recommend(created),
		MissingInputs:     []string{"chosen_move", "work_product", "evidence", "confidence"},
		NextPrompt:        "Inventory what is known, unknown, assumed, conflicting, checkable, or blocked, then choose the next cognitive move and report its visible work product.",
		KnowledgeState:    created.Ledger.clone(),
	}, nil
}

var defaultSessionCounter atomic.Uint64

func defaultSessionID() string {
	n := defaultSessionCounter.Add(1)
	return fmt.Sprintf("think-%d-%d", time.Now().UTC().UnixNano(), n)
}
