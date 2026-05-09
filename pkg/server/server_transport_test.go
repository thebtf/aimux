package server

import (
	"strings"
	"testing"
)

func TestHTTPTransportAuthPolicyRejectsMissingTokenByDefault(t *testing.T) {
	srv := &Server{log: newTestLogger(t)}

	err := srv.requireHTTPTransportAuth("HTTP", "127.0.0.1:8080")
	if err == nil {
		t.Fatal("expected missing auth token to fail closed")
	}
	if !strings.Contains(err.Error(), "AIMUX_AUTH_TOKEN") {
		t.Fatalf("error = %q, want token guidance", err)
	}
}

func TestHTTPTransportAuthPolicyAllowsExplicitLocalOverride(t *testing.T) {
	t.Setenv(allowUnauthenticatedHTTPEnv, "1")
	srv := &Server{log: newTestLogger(t)}

	if err := srv.requireHTTPTransportAuth("SSE", "localhost:8080"); err != nil {
		t.Fatalf("local dev override denied: %v", err)
	}
}

func TestHTTPTransportAuthPolicyRejectsNonLocalhostWithoutTokenEvenWithOverride(t *testing.T) {
	t.Setenv(allowUnauthenticatedHTTPEnv, "1")
	srv := &Server{log: newTestLogger(t)}

	err := srv.requireHTTPTransportAuth("HTTP", "192.0.2.10:8080")
	if err == nil {
		t.Fatal("expected non-localhost unauthenticated bind to be denied")
	}
	if !strings.Contains(err.Error(), "non-localhost") {
		t.Fatalf("error = %q, want non-localhost denial", err)
	}
}
