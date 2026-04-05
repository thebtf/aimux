package server_test

import (
	"testing"

	"github.com/thebtf/aimux/pkg/session"
	"github.com/thebtf/aimux/pkg/types"
)

// BenchmarkSessionCreate measures session creation throughput.
func BenchmarkSessionCreate(b *testing.B) {
	reg := session.NewRegistry()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		reg.Create("codex", types.SessionModeOnceStateful, "/tmp")
	}
}

// BenchmarkJobLifecycle measures full job lifecycle throughput.
func BenchmarkJobLifecycle(b *testing.B) {
	reg := session.NewRegistry()
	jm := session.NewJobManager()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sess := reg.Create("codex", types.SessionModeOnceStateful, "/tmp")
		job := jm.Create(sess.ID, "codex")
		jm.StartJob(job.ID, 0)
		jm.CompleteJob(job.ID, "content", 0)
	}
}

// BenchmarkPollCount measures poll increment throughput.
func BenchmarkPollCount(b *testing.B) {
	jm := session.NewJobManager()
	job := jm.Create("bench-session", "codex")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		jm.IncrementPoll(job.ID)
	}
}
