package conpty_test

import (
	"context"
	"strings"
	"testing"

	"github.com/thebtf/aimux/pkg/executor/conpty"
	"github.com/thebtf/aimux/pkg/types"
)

func TestConPTY_Start_ReturnsError(t *testing.T) {
	e := conpty.New()
	_, err := e.Start(context.Background(), types.SpawnArgs{
		Command: "echo",
		Args:    []string{"test"},
	})

	if err == nil {
		t.Fatal("expected error from Start() — ConPTY handles single-shot only")
	}

	errMsg := err.Error()
	if !strings.Contains(errMsg, "Pipe executor") {
		t.Errorf("error should mention Pipe executor, got: %s", errMsg)
	}
}

func TestConPTY_Run_Unavailable(t *testing.T) {
	e := conpty.New()
	if e.Available() {
		t.Skip("ConPTY available on this platform — test targets unavailable case")
	}

	_, err := e.Run(context.Background(), types.SpawnArgs{
		Command: "echo",
		Args:    []string{"test"},
	})

	if err == nil {
		t.Fatal("expected error when ConPTY unavailable")
	}
}
