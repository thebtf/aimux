package main

import (
	"errors"
	"testing"
)

func TestBuildControlRequestBlocksGracefulRestartByDefault(t *testing.T) {
	_, err := buildControlRequest("graceful-restart", 10000, false)
	if !errors.Is(err, errUnsafeGracefulRestart) {
		t.Fatalf("buildControlRequest() error = %v, want errUnsafeGracefulRestart", err)
	}
}

func TestBuildControlRequestAllowsExplicitUnsafeGracefulRestart(t *testing.T) {
	req, err := buildControlRequest("graceful-restart", 1234, true)
	if err != nil {
		t.Fatalf("buildControlRequest() error = %v", err)
	}
	if req.Cmd != "graceful-restart" {
		t.Fatalf("Cmd = %q, want graceful-restart", req.Cmd)
	}
	if req.DrainTimeoutMs != 1234 {
		t.Fatalf("DrainTimeoutMs = %d, want 1234", req.DrainTimeoutMs)
	}
}

func TestBuildControlRequestShutdownStillSetsDrainTimeout(t *testing.T) {
	req, err := buildControlRequest("shutdown", 4321, false)
	if err != nil {
		t.Fatalf("buildControlRequest() error = %v", err)
	}
	if req.Cmd != "shutdown" {
		t.Fatalf("Cmd = %q, want shutdown", req.Cmd)
	}
	if req.DrainTimeoutMs != 4321 {
		t.Fatalf("DrainTimeoutMs = %d, want 4321", req.DrainTimeoutMs)
	}
}

func TestBuildControlRequestStatusHasNoDrainTimeout(t *testing.T) {
	req, err := buildControlRequest("status", 4321, false)
	if err != nil {
		t.Fatalf("buildControlRequest() error = %v", err)
	}
	if req.Cmd != "status" {
		t.Fatalf("Cmd = %q, want status", req.Cmd)
	}
	if req.DrainTimeoutMs != 0 {
		t.Fatalf("DrainTimeoutMs = %d, want 0", req.DrainTimeoutMs)
	}
}
