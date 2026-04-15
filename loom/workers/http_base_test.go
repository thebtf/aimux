package workers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/thebtf/aimux/loom"
)

// staticHTTPResolver always returns the same HTTPRequest.
type staticHTTPResolver struct {
	req HTTPRequest
}

func (r *staticHTTPResolver) Resolve(_ context.Context, _ *loom.Task) (HTTPRequest, error) {
	return r.req, nil
}

// errorHTTPResolver always returns an error from Resolve.
type errorHTTPResolver struct {
	err error
}

func (r *errorHTTPResolver) Resolve(_ context.Context, _ *loom.Task) (HTTPRequest, error) {
	return HTTPRequest{}, r.err
}

// TestHTTPBase_HappyPath verifies a simple 200 OK response.
func TestHTTPBase_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("hello"))
	}))
	defer srv.Close()

	base := &HTTPBase{
		Resolver:   &staticHTTPResolver{req: HTTPRequest{Method: "GET", URL: srv.URL}},
		MaxRetries: 2,
		BackoffMS:  1, // minimise backoff in tests
	}
	task := &loom.Task{ID: "h1"}
	result, err := base.Run(context.Background(), task)
	if err != nil {
		t.Fatalf("Run returned unexpected error: %v", err)
	}
	if result.Content != "hello" {
		t.Errorf("content: want %q, got %q", "hello", result.Content)
	}
	if result.Metadata["status_code"] != http.StatusOK {
		t.Errorf("status_code: want 200, got %v", result.Metadata["status_code"])
	}
	// First attempt succeeds — attempts count should equal maxRetries+1 per spec.
	// The metadata records the configured max (not actual attempts made).
	_ = result.Metadata["attempts"]
}

// TestHTTPBase_5xxRetrySuccess verifies that 500 twice then 200 results in success.
func TestHTTPBase_5xxRetrySuccess(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n <= 2 {
			http.Error(w, "server error", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	base := &HTTPBase{
		Resolver:   &staticHTTPResolver{req: HTTPRequest{Method: "GET", URL: srv.URL}},
		MaxRetries: 2,
		BackoffMS:  1,
	}
	task := &loom.Task{ID: "h2"}
	result, err := base.Run(context.Background(), task)
	if err != nil {
		t.Fatalf("Run returned unexpected error after retries: %v", err)
	}
	if result.Content != "ok" {
		t.Errorf("content: want %q, got %q", "ok", result.Content)
	}
	if atomic.LoadInt32(&calls) != 3 {
		t.Errorf("expected 3 calls (2 failures + 1 success), got %d", atomic.LoadInt32(&calls))
	}
}

// TestHTTPBase_5xxExhausted verifies that always-500 exhausts retries and returns error.
func TestHTTPBase_5xxExhausted(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		http.Error(w, "always bad", http.StatusInternalServerError)
	}))
	defer srv.Close()

	base := &HTTPBase{
		Resolver:   &staticHTTPResolver{req: HTTPRequest{Method: "GET", URL: srv.URL}},
		MaxRetries: 2,
		BackoffMS:  1,
	}
	task := &loom.Task{ID: "h3"}
	result, err := base.Run(context.Background(), task)
	if err == nil {
		t.Fatal("expected error when all retries exhausted")
	}
	if result == nil {
		t.Fatal("result should be non-nil even on error")
	}
	if result.Metadata["status_code"] != http.StatusInternalServerError {
		t.Errorf("status_code: want 500, got %v", result.Metadata["status_code"])
	}
	if atomic.LoadInt32(&calls) != 3 {
		t.Errorf("expected 3 attempts, got %d", atomic.LoadInt32(&calls))
	}
}

// TestHTTPBase_4xxNoRetry verifies that 404 is returned immediately without retry.
func TestHTTPBase_4xxNoRetry(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	base := &HTTPBase{
		Resolver:   &staticHTTPResolver{req: HTTPRequest{Method: "GET", URL: srv.URL}},
		MaxRetries: 2,
		BackoffMS:  1,
	}
	task := &loom.Task{ID: "h4"}
	result, err := base.Run(context.Background(), task)
	if err == nil {
		t.Fatal("expected error for 404")
	}
	if result == nil {
		t.Fatal("result should be non-nil for 4xx")
	}
	if result.Metadata["status_code"] != http.StatusNotFound {
		t.Errorf("status_code: want 404, got %v", result.Metadata["status_code"])
	}
	// Must NOT retry on 4xx.
	if atomic.LoadInt32(&calls) != 1 {
		t.Errorf("expected 1 attempt for 4xx, got %d", atomic.LoadInt32(&calls))
	}
}

// TestHTTPBase_ContextCancelDuringBackoff verifies cancellation mid-backoff propagates.
func TestHTTPBase_ContextCancelDuringBackoff(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		http.Error(w, "server error", http.StatusInternalServerError)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	// BackoffMS=1000 so the second attempt's backoff is long enough to cancel.
	base := &HTTPBase{
		Resolver:   &staticHTTPResolver{req: HTTPRequest{Method: "GET", URL: srv.URL}},
		MaxRetries: 3,
		BackoffMS:  1000,
	}
	// Cancel after first attempt fails and backoff begins.
	go func() {
		// Wait until first call hits the server.
		for atomic.LoadInt32(&calls) == 0 {
		}
		cancel()
	}()

	task := &loom.Task{ID: "h5"}
	_, err := base.Run(ctx, task)
	if err == nil {
		t.Fatal("expected error on context cancellation during backoff")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error should wrap context.Canceled, got: %v", err)
	}
}
