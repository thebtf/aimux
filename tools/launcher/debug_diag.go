// Package main — debug_diag.go implements the diag-mode Send path for debugExecutor.
//
// sendViaSendStream is extracted here to keep debug_executor.go within the
// NFR-4 ≤ 300 LOC limit.  It is the only entry point; all types it uses are
// defined in debug_executor.go and jsonl.go.
package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/thebtf/aimux/pkg/types"
)

// sendViaSendStream is the diag path for Send.  It calls SendStream on the
// inner executor and aggregates per-line chunks into a complete Response.
// Each non-empty chunk is printed to stderr with an elapsed-time prefix.
// A heartbeat goroutine fires when no output arrives for heartbeatInterval.
func (d *debugExecutor) sendViaSendStream(ctx context.Context, msg types.Message) (*types.Response, error) {
	d.emitSpawnArgs(msg)
	fmt.Fprintf(os.Stderr, "[diag] starting realtime capture via SendStream\n")

	start := time.Now()
	hs := newHeartbeatState(start)
	stopHB := startHeartbeat(d.sink, hs)
	defer close(stopHB)

	info := d.inner.Info()
	streamLabel := "api_delta"
	if info.Type == types.ExecutorTypeCLI {
		streamLabel = "cli_line"
	}

	var aggregated strings.Builder

	wrappedChunk := func(c types.Chunk) {
		if c.Content != "" {
			elapsed := time.Since(start)
			fmt.Fprintf(os.Stderr, "[+%.1fs] %s", elapsed.Seconds(), c.Content)
			if !strings.HasSuffix(c.Content, "\n") {
				fmt.Fprintln(os.Stderr)
			}
			hs.touch()
			aggregated.WriteString(c.Content)
		}
		d.sink.Emit(KindChunk, chunkPayload{
			Content: c.Content,
			Done:    c.Done,
			Stream:  streamLabel,
		})
	}

	resp, err := d.inner.SendStream(ctx, msg, wrappedChunk)
	elapsed := time.Since(start)

	// If SendStream returned a response, use it; otherwise build one from
	// aggregated content so callers always get a non-nil Response on success.
	if resp == nil && err == nil {
		resp = &types.Response{
			Content:  aggregated.String(),
			Duration: elapsed,
		}
	} else if resp != nil && resp.Content == "" {
		// Some adapters return an empty Content in the Response when streaming;
		// fill it from the aggregated chunks so the complete event is useful.
		resp = &types.Response{
			Content:    aggregated.String(),
			ExitCode:   resp.ExitCode,
			TokensUsed: resp.TokensUsed,
			Duration:   elapsed,
		}
	}

	d.emitComplete(resp, err, elapsed)
	d.emitClassify(resp, err)
	d.emitBreakerState()
	d.emitCooldownState()

	return resp, err
}
