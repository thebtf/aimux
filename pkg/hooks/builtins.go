package hooks

import (
	"fmt"
	"os"
)

// NewTelemetryHook returns an after-execution hook that logs telemetry to stderr.
func NewTelemetryHook() AfterHookFn {
	return func(ctx AfterHookContext) AfterHookResult {
		msg := fmt.Sprintf("[aimux:telemetry] cli=%s exit=%d duration=%dms", ctx.CLI, ctx.ExitCode, ctx.DurationMs)
		if ctx.ExitCode != 0 {
			msg += " anomaly=non_zero_exit"
		}
		if ctx.Content == "" {
			msg += " anomaly=empty_output"
		}
		fmt.Fprintln(os.Stderr, msg)
		return AfterHookResult{Action: "accept"}
	}
}
