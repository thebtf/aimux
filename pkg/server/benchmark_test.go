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
