package server

import (
	"context"
	"fmt"
	"sort"
	"testing"
	"time"
)

func BenchmarkThinkHarnessFinalizeHandlerOverhead(b *testing.B) {
	srv := testServer(b)
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		b.StopTimer()
		sessionID := prepareThinkHarnessFinalizeSession(b, srv, i)
		request := makeRequest("think", map[string]any{
			"action":          "finalize",
			"session_id":      sessionID,
			"proposed_answer": "The supported answer can ship.",
		})
		b.StartTimer()
		result, err := srv.handleThinkHarness(context.Background(), request)
		b.StopTimer()
		if err != nil {
			b.Fatalf("finalize: %v", err)
		}
		if result.IsError {
			b.Fatalf("finalize returned tool error: %+v", result.Content)
		}
		payload := parseResult(b, result)
		if payload["can_finalize"] != true {
			b.Fatalf("finalize blocked: %v", payload)
		}
	}

	b.StopTimer()
	p95Srv := testServer(b)
	p95 := measureServerFinalizeP95(b, p95Srv, 32, 32)
	b.ReportMetric(float64(p95)/float64(time.Millisecond), "p95_ms")
}

func measureServerFinalizeP95(tb testing.TB, srv *Server, sampleCount int, batchSize int) time.Duration {
	tb.Helper()
	samples := make([]time.Duration, 0, sampleCount*batchSize)
	for sample := 0; sample < sampleCount; sample++ {
		sessionIDs := make([]string, batchSize)
		for i := 0; i < batchSize; i++ {
			sessionIDs[i] = prepareThinkHarnessFinalizeSession(tb, srv, sample*batchSize+i)
		}

		for i := 0; i < batchSize; i++ {
			request := makeRequest("think", map[string]any{
				"action":          "finalize",
				"session_id":      sessionIDs[i],
				"proposed_answer": "The supported answer can ship.",
			})
			start := time.Now()
			result, err := srv.handleThinkHarness(context.Background(), request)
			samples = append(samples, time.Since(start))
			if err != nil {
				tb.Fatalf("finalize: %v", err)
			}
			if result.IsError {
				tb.Fatalf("finalize returned tool error: %+v", result.Content)
			}
			payload := parseResult(tb, result)
			if payload["can_finalize"] != true {
				tb.Fatalf("finalize blocked during p95 sample: %v", payload)
			}
		}
	}
	return serverPercentileDuration(samples, 0.95)
}

func prepareThinkHarnessFinalizeSession(tb testing.TB, srv *Server, i int) string {
	tb.Helper()
	startResult, err := srv.handleThinkHarness(context.Background(), makeRequest("think", map[string]any{
		"action":          "start",
		"task":            fmt.Sprintf("benchmark finalize %d", i),
		"context_summary": "prepare a supported answer before timing finalize",
	}))
	if err != nil {
		tb.Fatalf("start: %v", err)
	}
	if startResult.IsError {
		tb.Fatalf("start returned error: %+v", startResult.Content)
	}
	startPayload := parseResult(tb, startResult)
	sessionID, _ := startPayload["session_id"].(string)
	if sessionID == "" {
		tb.Fatalf("start missing session_id: %v", startPayload)
	}

	stepResult, err := srv.handleThinkHarness(context.Background(), makeRequest("think", map[string]any{
		"action":       "step",
		"session_id":   sessionID,
		"chosen_move":  "critical_thinking",
		"work_product": "The answer has visible support.",
		"confidence":   0.78,
		"evidence": []any{map[string]any{
			"kind":                "file",
			"ref":                 "spec.md",
			"summary":             "finalization requires visible support",
			"verification_status": "verified",
		}},
	}))
	if err != nil {
		tb.Fatalf("step: %v", err)
	}
	if stepResult.IsError {
		tb.Fatalf("step returned error: %+v", stepResult.Content)
	}
	return sessionID
}

func serverPercentileDuration(values []time.Duration, percentile float64) time.Duration {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]time.Duration(nil), values...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	idx := int(float64(len(sorted)-1) * percentile)
	return sorted[idx]
}
