package main

import (
	"flag"
	"fmt"
	"os"
	"time"
)

// runSlowCodex emulates a codex-like process with three output-timing modes,
// selected by the -mode flag. All modes print plain-text lines to stdout
// (unbuffered for subprocess pipes) so the pipe executor's IOManager delivers
// each line to SpawnArgs.OnOutput as it arrives.
//
// Modes:
//
//	burst      (default) — three lines with 200ms pauses, then exit. The original
//	                        progress_tail polling behavior.
//	long-legit — emit one line every -interval for -duration, then exit. Simulates
//	             legitimate long-running work that keeps producing output: the
//	             stall detector must NEVER flag this as a hang because silence
//	             never exceeds the soft-warning threshold between lines.
//	hang       — emit an initial line (or nothing if -silent-start), then go
//	             silent for -duration WITHOUT exiting. Simulates a wedged process:
//	             the stall detector MUST flag this once silence crosses the
//	             configured tiers.
//	mid-hang   — emit -lines lines (one per -interval), then go silent for
//	             -duration without exiting. Simulates a process that started
//	             streaming and then wedged mid-turn: the artifact-aware window
//	             (#359 C2) must flag this once active-soft silence is crossed,
//	             faster than the startup budget would.
//
// This split is the emulator half of the #359 "hang != long-legit-work"
// invariant: a working CLI always keeps producing content; a hung one goes
// silent. The stall-detection playbook drives both modes through the real
// task/leaf-CLI dispatch path and asserts the detector distinguishes them.
//
// Flags:
//
//	-p <prompt>       prompt text (ignored; flag compatibility)
//	-mode <mode>      burst | long-legit | hang (default burst)
//	-interval <dur>   inter-line pause for long-legit (default 200ms)
//	-duration <dur>   total emit window (long-legit) or silence window (hang)
//	-silent-start     hang mode: emit no initial line (silent from dispatch)
func runSlowCodex() int {
	fs := flag.NewFlagSet("slow-codex", flag.ContinueOnError)
	prompt := fs.String("p", "", "prompt (ignored)")
	mode := fs.String("mode", "burst", "output timing mode: burst | long-legit | hang")
	interval := fs.Duration("interval", 200*time.Millisecond, "inter-line pause (long-legit)")
	duration := fs.Duration("duration", 2*time.Second, "emit window (long-legit) or silence window (hang)")
	silentStart := fs.Bool("silent-start", false, "hang mode: emit no initial line")
	linesBeforeHang := fs.Int("lines", 3, "mid-hang mode: lines to emit before going silent")

	if err := fs.Parse(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "slow-codex: %v\n", err)
		return 1
	}
	_ = prompt

	switch *mode {
	case "long-legit":
		return runSlowCodexLongLegit(*interval, *duration)
	case "hang":
		return runSlowCodexHang(*duration, *silentStart)
	case "mid-hang":
		return runSlowCodexMidHang(*linesBeforeHang, *interval, *duration)
	default: // burst
		return runSlowCodexBurst()
	}
}

// runSlowCodexBurst prints three lines with 200ms pauses, then exits.
func runSlowCodexBurst() int {
	for _, l := range []string{"line 1", "line 2", "line 3"} {
		fmt.Println(l)
		time.Sleep(200 * time.Millisecond)
	}
	return 0
}

// runSlowCodexLongLegit emits a progress line every interval until duration
// elapses, then exits 0. Silence between lines never exceeds interval, so a
// correctly-configured stall detector (soft-warning > interval) must not flag it.
func runSlowCodexLongLegit(interval, duration time.Duration) int {
	if interval <= 0 {
		interval = 200 * time.Millisecond
	}
	deadline := time.Now().Add(duration)
	n := 0
	for time.Now().Before(deadline) {
		n++
		fmt.Printf("working: step %d\n", n)
		time.Sleep(interval)
	}
	fmt.Println("working: done")
	return 0
}

// runSlowCodexHang emits an initial line (unless silentStart) then blocks
// silently for duration WITHOUT producing further output. It exits 0 after the
// silence window so the test process never leaks, but during the window the
// stall detector must observe growing silence and cross its tiers.
func runSlowCodexHang(duration time.Duration, silentStart bool) int {
	if !silentStart {
		fmt.Println("starting up")
	}
	time.Sleep(duration)
	return 0
}

// runSlowCodexMidHang emits `lines` lines (one per interval) then goes silent
// for duration without exiting. It models a process that began streaming and
// wedged mid-turn: because output DID start, ProgressUpdatedAt is set and the
// artifact-aware active-soft window (#359 C2) applies, flagging the silence
// faster than the startup soft-warning would.
func runSlowCodexMidHang(lines int, interval, duration time.Duration) int {
	if lines < 1 {
		lines = 1
	}
	if interval <= 0 {
		interval = 200 * time.Millisecond
	}
	for i := 1; i <= lines; i++ {
		fmt.Printf("progress: %d\n", i)
		time.Sleep(interval)
	}
	time.Sleep(duration)
	return 0
}
