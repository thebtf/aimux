package harness

import (
	"errors"
	"testing"
)

func TestControllerStepGuidanceOnlyDoesNotMutateSession(t *testing.T) {
	controller := NewController(NewInMemoryStore(), WithIDGenerator(func() string { return "think-guide-1" }))
	start, err := controller.Start(t.Context(), StartRequest{
		Task:           "choose next evidence",
		ContextSummary: "no move has run yet",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	execute := false
	resp, err := controller.Step(t.Context(), StepRequest{
		SessionID:  start.SessionID,
		ChosenMove: "source_comparison",
		Execute:    &execute,
	})
	if err != nil {
		t.Fatalf("Step guidance-only: %v", err)
	}
	if resp.Executed {
		t.Fatal("guidance-only step must not be marked executed")
	}
	if len(resp.RequiredReportBack) == 0 {
		t.Fatal("guidance-only step must name report-back fields")
	}

	session, err := controller.Session(t.Context(), start.SessionID)
	if err != nil {
		t.Fatalf("Session: %v", err)
	}
	if len(session.MoveHistory) != 0 || len(session.Observations) != 0 {
		t.Fatalf("guidance-only mutated session: %+v", session)
	}
}

func TestControllerStepMoveMismatchFailsClosed(t *testing.T) {
	controller := NewController(NewInMemoryStore(), WithIDGenerator(func() string { return "think-mismatch-1" }))
	start, err := controller.Start(t.Context(), StartRequest{
		Task:           "pick a move",
		ContextSummary: "invalid move should fail closed",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	_, err = controller.Step(t.Context(), StepRequest{
		SessionID:   start.SessionID,
		ChosenMove:  "debug_workflow",
		WorkProduct: "should not matter",
	})
	if !errors.Is(err, ErrMoveMismatch) {
		t.Fatalf("Step error = %v, want ErrMoveMismatch", err)
	}

	session, err := controller.Session(t.Context(), start.SessionID)
	if err != nil {
		t.Fatalf("Session: %v", err)
	}
	if len(session.MoveHistory) != 0 || len(session.Observations) != 0 {
		t.Fatalf("move mismatch mutated session: %+v", session)
	}
}
