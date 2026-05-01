// replay.go implements the 'launcher replay' subcommand — a JSONL log reader
// with optional kind filtering and two output modes: human-readable (default)
// and raw NDJSON (--raw).
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
)

// runReplay reads a JSONL log written by the launcher and prints events to
// stdout in human-readable form (default) or raw NDJSON (--raw). Events not
// matching --filter (comma-separated list of kinds) are skipped.
//
// Returns OS exit code.
func runReplay(args []string) int {
	fs := flag.NewFlagSet("launcher replay", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	logPath := fs.String("log", "", "path to JSONL log file (required)")
	filter := fs.String("filter", "", "comma-separated list of event kinds to include (empty = all)")
	raw := fs.Bool("raw", false, "re-emit NDJSON byte-identical to input (overrides human-readable)")

	if err := fs.Parse(args); err != nil {
		return 2
	}

	if *logPath == "" {
		fmt.Fprintln(os.Stderr, "launcher replay: --log is required")
		fs.Usage()
		return 2
	}

	// Build filter set once; nil map means "accept all".
	var filterSet map[string]struct{}
	if *filter != "" {
		filterSet = make(map[string]struct{})
		for _, k := range strings.Split(*filter, ",") {
			k = strings.TrimSpace(k)
			if k != "" {
				filterSet[k] = struct{}{}
			}
		}
	}

	f, err := os.Open(*logPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "launcher replay: %v\n", err)
		return 1
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	// 1 MB max line buffer — matches IOManager in pkg/executor/iomanager.go:55.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := scanner.Text()
		if line == "" {
			continue
		}

		var ev Event
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			fmt.Fprintf(os.Stderr, "[replay] line %d: parse error: %v\n", lineNum, err)
			continue
		}

		// Apply kind filter.
		if filterSet != nil {
			if _, ok := filterSet[ev.Kind]; !ok {
				continue
			}
		}

		if *raw {
			fmt.Println(line)
			continue
		}

		fmt.Println(formatEvent(ev))
	}

	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "launcher replay: scan error: %v\n", err)
		return 1
	}

	return 0
}

// formatEvent renders one Event as a human-readable line.
// Each kind gets a dedicated format; unknown kinds fall back to compact JSON payload.
func formatEvent(ev Event) string {
	prefix := fmt.Sprintf("[%d] %s:", ev.Seq, ev.Kind)

	switch ev.Kind {
	case KindSpawnArgs:
		var p spawnArgsPayload
		if err := json.Unmarshal(ev.Payload, &p); err != nil {
			return formatFallback(prefix, ev.Payload)
		}
		return fmt.Sprintf("%s command=%s args=%v executor=%s", prefix, p.Command, p.Args, p.Executor)

	case KindComplete:
		var p completePayload
		if err := json.Unmarshal(ev.Payload, &p); err != nil {
			return formatFallback(prefix, ev.Payload)
		}
		base := fmt.Sprintf("%s exit=%d duration=%dms tokens=%d/%d",
			prefix, p.ExitCode, p.DurationMs, p.TokensUsed.Input, p.TokensUsed.Output)
		if p.Error != "" {
			return base + " error: " + truncate(p.Error, 100)
		}
		return base

	case KindClassify:
		var p classifyPayload
		if err := json.Unmarshal(ev.Payload, &p); err != nil {
			return formatFallback(prefix, ev.Payload)
		}
		return fmt.Sprintf("%s class=%s code=%d", prefix, p.Class, p.ClassCode)

	case KindBreakerState:
		var p breakerStatePayload
		if err := json.Unmarshal(ev.Payload, &p); err != nil {
			return formatFallback(prefix, ev.Payload)
		}
		return fmt.Sprintf("%s cli=%s state=%s failures=%d", prefix, p.CLI, p.State, p.Failures)

	case KindCooldownState:
		var p cooldownStatePayload
		if err := json.Unmarshal(ev.Payload, &p); err != nil {
			return formatFallback(prefix, ev.Payload)
		}
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("%s count=%d", prefix, p.Count))
		for _, e := range p.Entries {
			sb.WriteString(fmt.Sprintf(" [cli=%s model=%s expires=%s]", e.CLI, e.Model, e.ExpiresAt.String()))
		}
		return sb.String()

	case KindChunk:
		var p chunkPayload
		if err := json.Unmarshal(ev.Payload, &p); err != nil {
			return formatFallback(prefix, ev.Payload)
		}
		return fmt.Sprintf("%s stream=%s done=%v content=%s", prefix, p.Stream, p.Done, truncate(p.Content, 60))

	case KindStdout, KindStderr:
		var p stdoutPayload
		if err := json.Unmarshal(ev.Payload, &p); err != nil {
			return formatFallback(prefix, ev.Payload)
		}
		text := p.Content
		if text == "" {
			text = p.BytesHex
		}
		return fmt.Sprintf("%s stream=%s %s", prefix, p.Stream, truncate(text, 80))

	case KindSpawn:
		var p spawnPayload
		if err := json.Unmarshal(ev.Payload, &p); err != nil {
			return formatFallback(prefix, ev.Payload)
		}
		return fmt.Sprintf("%s pid=%d", prefix, p.PID)

	case KindExit:
		var p exitPayload
		if err := json.Unmarshal(ev.Payload, &p); err != nil {
			return formatFallback(prefix, ev.Payload)
		}
		return fmt.Sprintf("%s code=%d", prefix, p.ExitCode)

	case KindTurn:
		var p turnPayload
		if err := json.Unmarshal(ev.Payload, &p); err != nil {
			return formatFallback(prefix, ev.Payload)
		}
		return fmt.Sprintf("%s tid=%d role=%s content=%s", prefix, p.TurnID, p.Role, truncate(p.Content, 60))

	case KindError:
		var p errorPayload
		if err := json.Unmarshal(ev.Payload, &p); err != nil {
			return formatFallback(prefix, ev.Payload)
		}
		base := fmt.Sprintf("%s source=%s signal=%s message=%s", prefix, p.Source, p.Signal, truncate(p.Message, 100))
		return base

	case KindHTTPRequest:
		var m map[string]json.RawMessage
		if err := json.Unmarshal(ev.Payload, &m); err != nil {
			return formatFallback(prefix, ev.Payload)
		}
		method, _ := jsonString(m["method"])
		url, _ := jsonString(m["url"])
		return fmt.Sprintf("%s method=%s url=%s", prefix, method, truncate(url, 80))

	case KindHTTPResponse:
		var m map[string]json.RawMessage
		if err := json.Unmarshal(ev.Payload, &m); err != nil {
			return formatFallback(prefix, ev.Payload)
		}
		status, _ := jsonString(m["status"])
		return fmt.Sprintf("%s status=%s", prefix, status)

	default:
		return formatFallback(prefix, ev.Payload)
	}
}

// formatFallback renders the payload as compact JSON for unknown or unparseable kinds.
func formatFallback(prefix string, payload json.RawMessage) string {
	compact, err := json.Marshal(json.RawMessage(payload))
	if err != nil {
		return prefix + " <unparseable payload>"
	}
	return prefix + " " + string(compact)
}

// jsonString extracts a string value from a raw JSON message (e.g. `"foo"` → `foo`).
func jsonString(raw json.RawMessage) (string, bool) {
	if raw == nil {
		return "", false
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return string(raw), false
	}
	return s, true
}
