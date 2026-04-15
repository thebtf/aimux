package workers

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/thebtf/aimux/loom"
)

// HTTPRequest is the aimux-agnostic HTTP request descriptor.
type HTTPRequest struct {
	Method  string
	URL     string
	Headers map[string]string
	Body    []byte
}

// HTTPResolver builds an HTTPRequest from a task at execution time.
type HTTPResolver interface {
	Resolve(ctx context.Context, task *loom.Task) (HTTPRequest, error)
}

// HTTPBase is a composable base for workers that make HTTP calls with retry.
type HTTPBase struct {
	Resolver   HTTPResolver
	Client     *http.Client // optional, defaults to &http.Client{Timeout: 30s}
	MaxRetries int          // 0 = no retry, default 2
	BackoffMS  int          // base backoff in ms, default 500
}

// Run executes the HTTP request with exponential backoff retry for transient errors.
// 5xx responses and network errors are retried. 4xx responses are returned immediately
// without retry — the caller's bug, not a transient issue.
func (b *HTTPBase) Run(ctx context.Context, task *loom.Task) (*loom.WorkerResult, error) {
	req, err := b.Resolver.Resolve(ctx, task)
	if err != nil {
		return nil, fmt.Errorf("http: resolve: %w", err)
	}

	client := b.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	maxRetries := b.MaxRetries
	if maxRetries == 0 {
		maxRetries = 2
	}
	backoffMS := b.BackoffMS
	if backoffMS == 0 {
		backoffMS = 500
	}

	start := time.Now()
	var lastErr error
	var lastResp *http.Response
	var lastBody []byte

	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			delay := time.Duration(backoffMS*(1<<(attempt-1))) * time.Millisecond
			select {
			case <-ctx.Done():
				return nil, fmt.Errorf("http: %w", ctx.Err())
			case <-time.After(delay):
			}
		}

		httpReq, buildErr := http.NewRequestWithContext(ctx, req.Method, req.URL, bytes.NewReader(req.Body))
		if buildErr != nil {
			return nil, fmt.Errorf("http: build request: %w", buildErr)
		}
		for k, v := range req.Headers {
			httpReq.Header.Set(k, v)
		}

		resp, doErr := client.Do(httpReq)
		if doErr != nil {
			lastErr = doErr
			continue // transient network error — retry
		}
		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			continue
		}
		lastResp = resp
		lastBody = body
		lastErr = nil

		if resp.StatusCode >= 500 {
			lastErr = fmt.Errorf("http %d: %s", resp.StatusCode, string(body))
			continue // retry 5xx
		}
		break // success or 4xx — no retry
	}

	duration := time.Since(start).Milliseconds()

	if lastResp == nil {
		return nil, fmt.Errorf("http: all %d attempts failed: %w", maxRetries+1, lastErr)
	}

	result := &loom.WorkerResult{
		Content: string(lastBody),
		Metadata: map[string]any{
			"status_code": lastResp.StatusCode,
			"attempts":    maxRetries + 1,
		},
		DurationMS: duration,
	}
	if lastResp.StatusCode >= 400 {
		return result, fmt.Errorf("http %d", lastResp.StatusCode)
	}
	return result, nil
}
