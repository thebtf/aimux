// Package main implements persistent_testcli — a minimal echo CLI used by
// AIMUX-14 critical-suite tests to validate persistent-session NFR semantics
// (cold start, warm-send overhead, throughput, memory ceiling).
//
// Behavior: read stdin line-by-line; for each line, emit "echo: <line>" to
// stdout followed by a sentinel marker line. The sentinel terminates each
// turn's response so BaseSession's reader can match it.
//
// Optional env var PERSISTENT_TESTCLI_SENTINEL overrides the sentinel string
// (default: "===END==="). PERSISTENT_TESTCLI_SLEEP_MS adds artificial delay
// per turn for cold-start simulation.
package main

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"time"
)

func main() {
	sentinel := "===END==="
	if v := os.Getenv("PERSISTENT_TESTCLI_SENTINEL"); v != "" {
		sentinel = v
	}

	var perTurnDelay time.Duration
	if v := os.Getenv("PERSISTENT_TESTCLI_SLEEP_MS"); v != "" {
		if ms, err := strconv.Atoi(v); err == nil && ms > 0 {
			perTurnDelay = time.Duration(ms) * time.Millisecond
		}
	}

	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if perTurnDelay > 0 {
			time.Sleep(perTurnDelay)
		}
		fmt.Printf("echo: %s\n", line)
		fmt.Println(sentinel)
	}
}
