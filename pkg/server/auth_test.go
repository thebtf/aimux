package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAuthMiddleware_ValidToken(t *testing.T) {
	handlerCalled := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	})

	h := bearerAuthMiddleware("secret123", next)
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
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("next handler must not be called on invalid token")
	})

	h := bearerAuthMiddleware("secret123", next)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer wrong-token")
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

func TestAuthMiddleware_NoToken(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("next handler must not be called when Authorization header is absent")
	})

	h := bearerAuthMiddleware("secret123", next)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	// No Authorization header set
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

func TestAuthMiddleware_EmptyConfig(t *testing.T) {
	handlerCalled := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	})

	// Empty token → no auth, handler returned as-is.
	h := bearerAuthMiddleware("", next)
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
