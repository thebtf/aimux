package orchestrator_test

import (
	"testing"

	"github.com/thebtf/aimux/pkg/logger"
	"github.com/thebtf/aimux/pkg/orchestrator"
)

func newTestLogger(t *testing.T) *logger.Logger {
	t.Helper()
	log, err := logger.New(t.TempDir()+"/test.log", logger.LevelError)
	if err != nil {
		t.Fatalf("logger: %v", err)
	}
	t.Cleanup(func() { log.Close() })
	return log
}

func TestAuditPipeline_Name(t *testing.T) {
	mock := &mockExecutor{runResult: nil, runErr: nil}
	audit := orchestrator.NewAuditPipeline(mock)

	if audit.Name() != "audit" {
		t.Errorf("Name = %q, want audit", audit.Name())
	}
}
