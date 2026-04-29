package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/thebtf/aimux/pkg/logger"
)

// newTestLogger creates a logger writing to a temp file for testing.
func newTestLogger(t *testing.T) *logger.Logger {
	t.Helper()
	dir := t.TempDir()
	log, err := logger.New(filepath.Join(dir, "test.log"), logger.LevelDebug, logger.RotationOpts{})
	if err != nil {
		t.Fatalf("create test logger: %v", err)
	}
	t.Cleanup(func() { log.Close() })
	return log
}

func TestAuthMiddleware_ValidToken(t *testing.T) {
	log := newTestLogger(t)
	handlerCalled := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	})

	h := bearerAuthMiddleware("secret123", log, next)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer secret123")
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if !handlerCalled {
		t.Fatal("expected next handler to be called, but it was not")
	}
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
}

func TestAuthMiddleware_InvalidToken(t *testing.T) {
	log := newTestLogger(t)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("next handler must not be called on invalid token")
	})

	h := bearerAuthMiddleware("secret123", log, next)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer wrong-token")
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

func TestAuthMiddleware_NoToken(t *testing.T) {
	log := newTestLogger(t)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("next handler must not be called when Authorization header is absent")
	})

	h := bearerAuthMiddleware("secret123", log, next)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	// No Authorization header set
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

func TestAuthMiddleware_EmptyConfig(t *testing.T) {
	log := newTestLogger(t)
	handlerCalled := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	})

	// Empty token → no auth, handler returned as-is.
	h := bearerAuthMiddleware("", log, next)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	// No auth header — should pass through since auth is disabled.
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if !handlerCalled {
		t.Fatal("expected next handler to be called when auth is disabled")
	}
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
}

// TestAuthMiddleware_LogsOnMissingHeader verifies that a warning is written to the log
// file when the Authorization header is absent.
func TestAuthMiddleware_LogsOnMissingHeader(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "auth.log")
	log, err := logger.New(logPath, logger.LevelDebug, logger.RotationOpts{})
	if err != nil {
		t.Fatalf("create logger: %v", err)
	}
	defer log.Close()

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
	h := bearerAuthMiddleware("secret123", log, next)
	req := httptest.NewRequest(http.MethodGet, "/sse", nil)
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	// Flush async log writes.
	log.Close()

	content, readErr := os.ReadFile(logPath)
	if readErr != nil {
		t.Fatalf("read log: %v", readErr)
	}
	if len(content) == 0 {
		t.Error("expected log output on missing Authorization header, got empty log")
	}
}

// TestAuthMiddleware_LogsOnMismatch verifies that a warning is written when the token
// does not match.
func TestAuthMiddleware_LogsOnMismatch(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "auth.log")
	log, err := logger.New(logPath, logger.LevelDebug, logger.RotationOpts{})
	if err != nil {
		t.Fatalf("create logger: %v", err)
	}
	defer log.Close()

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
	h := bearerAuthMiddleware("secret123", log, next)
	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer wrong-token")
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	// Flush async log writes.
	log.Close()

	content, readErr := os.ReadFile(logPath)
	if readErr != nil {
		t.Fatalf("read log: %v", readErr)
	}
	if len(content) == 0 {
		t.Error("expected log output on token mismatch, got empty log")
	}
}
