package harness

import (
	"context"
	"fmt"
	"sort"
	"testing"
	"time"
)

func TestHarnessFinalizeOverheadP95(t *testing.T) {
	p95 := measureHarnessFinalizeP95(t, 64, 32)
	if p95 > 25*time.Millisecond {
		t.Fatalf("finalize p95 = %s, want <= 25ms", p95)
	}
}

func BenchmarkHarnessFinalizeGateEvaluation(b *testing.B) {
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		b.StopTimer()
		controller, sessionID := supportedFinalizeController(b, fmt.Sprintf("bench-%d", i))
		b.StartTimer()
		resp, err := controller.Finalize(context.Background(), FinalizeRequest{
			SessionID:      sessionID,
			ProposedAnswer: "The supported answer can ship.",
		})
		b.StopTimer()
		if err != nil {
			b.Fatalf("Finalize: %v", err)
		}
		if !resp.CanFinalize {
			b.Fatalf("Finalize blocked: %+v", resp)
		}
	}

	b.StopTimer()
	p95 := measureHarnessFinalizeP95(b, 32, 32)
	b.ReportMetric(float64(p95)/float64(time.Millisecond), "p95_ms")
}

func measureHarnessFinalizeP95(tb testing.TB, sampleCount int, batchSize int) time.Duration {
	tb.Helper()
	samples := make([]time.Duration, 0, sampleCount)
	for sample := 0; sample < sampleCount; sample++ {
		controllers := make([]*Controller, batchSize)
		sessionIDs := make([]string, batchSize)
		for i := 0; i < batchSize; i++ {
			id := fmt.Sprintf("p95-%d-%d", sample, i)
			controllers[i], sessionIDs[i] = supportedFinalizeController(tb, id)
		}

		start := time.Now()
		for i := 0; i < batchSize; i++ {
			resp, err := controllers[i].Finalize(context.Background(), FinalizeRequest{
				SessionID:      sessionIDs[i],
				ProposedAnswer: "The supported answer can ship.",
			})
			if err != nil {
				tb.Fatalf("Finalize: %v", err)
			}
			if !resp.CanFinalize {
				tb.Fatalf("Finalize blocked: %+v", resp)
			}
		}
		samples = append(samples, time.Since(start)/time.Duration(batchSize))
	}
	return percentileDuration(samples, 0.95)
}

func supportedFinalizeController(tb testing.TB, sessionID string) (*Controller, string) {
	tb.Helper()

	store := NewInMemoryStore()
	controller := NewController(store)
	frame := validFrameFromTB(tb)
	session := NewThinkingSession(sessionID, frame)
	updated, err := session.ApplyPatch(KnowledgePatch{
		Phase: PhaseIntegrate,
		LedgerAdds: KnowledgeLedger{
			Known: []LedgerEntry{{
				ID:     "answer",
				Text:   "The answer has visible support.",
				Source: "caller",
				Status: "observed",
			}},
		},
		Move: &MovePlan{
			Name:                  "critical_thinking",
			Group:                 MoveGroupEvaluate,
			Reason:                "verify the answer before finalization",
			ExpectedArtifactDelta: "finalization gate input is complete",
			Execute:               true,
		},
		Observation: &Observation{
			MoveName:    "critical_thinking",
			WorkProduct: "The answer has visible support.",
			Evidence: []EvidenceRef{{
				Kind:               "file",
				Ref:                "spec.md",
				Summary:            "finalization requires visible support",
				VerificationStatus: "verified",
			}},
			CallerConfidence: 0.78,
		},
		GateReport: &GateReport{Status: GatePass},
		StopDecision: &StopDecision{
			Action:      StopContinue,
			Reason:      "ready for finalization gate",
			CanFinalize: false,
		},
	})
	if err != nil {
		tb.Fatalf("apply patch: %v", err)
	}
	if _, err := store.Create(context.Background(), updated); err != nil {
		tb.Fatalf("create session: %v", err)
	}
	return controller, sessionID
}

func validFrameFromTB(tb testing.TB) TaskFrame {
	tb.Helper()
	frame, err := NewTaskFrame(TaskFrame{
		Task:           "verify finalization overhead",
		Goal:           "measure gate evaluation without pattern execution",
		ContextSummary: "supported session is pre-built",
		SuccessSignal:  "finalize returns within overhead budget",
	})
	if err != nil {
		tb.Fatalf("valid frame: %v", err)
	}
	return frame
}

func percentileDuration(values []time.Duration, percentile float64) time.Duration {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]time.Duration(nil), values...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	idx := int(float64(len(sorted)-1) * percentile)
	return sorted[idx]
}
