package codex

import "testing"

func TestForClass_ReviewPolicy(t *testing.T) {
	cfg, err := ForClass(JobClassReview)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Mode != SandboxModeReadOnly {
		t.Errorf("expected read-only, got %q", cfg.Mode)
	}
	if cfg.AskForApproval != AskForApprovalNever {
		t.Errorf("expected never, got %q", cfg.AskForApproval)
	}
}

func TestForClass_TaskPolicy(t *testing.T) {
	cfg, err := ForClass(JobClassTask)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Mode != SandboxModeReadOnly {
		t.Errorf("expected read-only, got %q", cfg.Mode)
	}
	if cfg.AskForApproval != AskForApprovalNever {
		t.Errorf("expected never, got %q", cfg.AskForApproval)
	}
}

func TestForClass_WriteTaskPolicy(t *testing.T) {
	cfg, err := ForClass(JobClassWriteTask)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Mode != SandboxModeWorkspaceWrite {
		t.Errorf("expected workspace-write, got %q", cfg.Mode)
	}
	if cfg.AskForApproval != AskForApprovalNever {
		t.Errorf("expected never, got %q", cfg.AskForApproval)
	}
}

func TestForClass_DangerPolicy(t *testing.T) {
	cfg, err := ForClass(JobClassDanger)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Mode != SandboxModeDangerFullAccess {
		t.Errorf("expected danger-full-access, got %q", cfg.Mode)
	}
	if cfg.AskForApproval != AskForApprovalOnRequest {
		t.Errorf("expected on-request, got %q", cfg.AskForApproval)
	}
}

func TestForClass_UnknownReturnsError(t *testing.T) {
	_, err := ForClass("bogus")
	if err == nil {
		t.Error("expected error for unknown class")
	}
}

func TestForClass_EmptyReturnsError(t *testing.T) {
	_, err := ForClass("")
	if err == nil {
		t.Error("expected error for empty class")
	}
}
