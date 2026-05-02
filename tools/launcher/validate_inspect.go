package main

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

func missingKinds(logPath string, required []string) ([]string, error) {
	seen := map[string]bool{}
	if err := scanEvents(logPath, func(ev logEventLite) error {
		seen[ev.Kind] = true
		return nil
	}); err != nil {
		return nil, err
	}
	var missing []string
	for _, kind := range required {
		if !seen[kind] {
			missing = append(missing, kind)
		}
	}
	return missing, nil
}

func inspectANSIProof(logPath string) (bool, bool, error) {
	rawANSI := false
	stripped := false
	err := scanEvents(logPath, func(ev logEventLite) error {
		if ev.Kind != KindStdout {
			return nil
		}
		var p stdoutPayload
		if err := json.Unmarshal(ev.Payload, &p); err != nil {
			return err
		}
		switch p.Stream {
		case "raw":
			rawBytes, err := hex.DecodeString(p.BytesHex)
			if err != nil {
				return err
			}
			if bytes.Contains(rawBytes, []byte{0x1b, 0x5b}) {
				rawANSI = true
			}
		case "line":
			if strings.Contains(p.Content, "AIMUX_ANSI_OK") && !strings.Contains(p.Content, "\x1b[") {
				stripped = true
			}
		}
		return nil
	})
	return rawANSI, stripped, err
}

func inspectRawByteCount(logPath string) (int64, int64, error) {
	info, err := os.Stat(logPath)
	if err != nil {
		return 0, 0, err
	}
	var total int64
	err = scanEvents(logPath, func(ev logEventLite) error {
		if ev.Kind != KindStdout {
			return nil
		}
		var p stdoutPayload
		if err := json.Unmarshal(ev.Payload, &p); err != nil {
			return err
		}
		if p.Stream == "raw" {
			decoded, err := hex.DecodeString(p.BytesHex)
			if err != nil {
				return err
			}
			total += int64(len(decoded))
		}
		return nil
	})
	return total, info.Size(), err
}

func hasTurnRoles(logPath string) (bool, error) {
	roles := map[string]bool{}
	if err := scanEvents(logPath, func(ev logEventLite) error {
		if ev.Kind != KindTurn {
			return nil
		}
		var p turnPayload
		if err := json.Unmarshal(ev.Payload, &p); err != nil {
			return err
		}
		roles[p.Role] = true
		return nil
	}); err != nil {
		return false, err
	}
	return roles["user"] && roles["agent"], nil
}

func turnContentContains(logPath, role, token string) (bool, error) {
	found := false
	if err := scanEvents(logPath, func(ev logEventLite) error {
		if ev.Kind != KindTurn {
			return nil
		}
		var p turnPayload
		if err := json.Unmarshal(ev.Payload, &p); err != nil {
			return err
		}
		if p.Role == role && strings.Contains(p.Content, token) {
			found = true
		}
		return nil
	}); err != nil {
		return false, err
	}
	return found, nil
}

func scanEvents(logPath string, fn func(logEventLite) error) error {
	f, err := os.Open(logPath)
	if err != nil {
		return err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), rawSpawnMaxLineBytes*2)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		var ev logEventLite
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			return fmt.Errorf("parse %s line %d: %w", logPath, lineNo, err)
		}
		if err := fn(ev); err != nil {
			return fmt.Errorf("inspect %s line %d: %w", logPath, lineNo, err)
		}
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("scan %s after line %d: %w", logPath, lineNo, err)
	}
	return nil
}

func classifyExternalBlocker(name, cmd, logPath, stdout, stderr string, code int, err error) ScenarioResult {
	combined := strings.ToLower(stdout + "\n" + stderr + "\n" + fmt.Sprint(err))
	detail := trim(stderr + " " + fmt.Sprint(err))
	reason := ""
	switch {
	case strings.Contains(combined, "executable file not found") || strings.Contains(combined, "cannot find the file") || strings.Contains(combined, "command not found") || strings.Contains(combined, "file not found") || strings.Contains(combined, "no such file or directory") || strings.Contains(combined, "not configured"):
		reason = "binary or profile unavailable: " + detail
	case strings.Contains(combined, "unauthorized") || strings.Contains(combined, "authentication") || strings.Contains(combined, "api key") || strings.Contains(combined, "login") || strings.Contains(combined, "credential"):
		reason = "authentication unavailable: " + detail
	case strings.Contains(combined, "network") || strings.Contains(combined, "connection") || strings.Contains(combined, "timeout") || strings.Contains(combined, "deadline exceeded"):
		reason = "network or timeout blocked scenario: " + detail
	case strings.Contains(combined, "quota") || strings.Contains(combined, "rate limit"):
		reason = "quota or rate limit blocked scenario: " + detail
	}
	if reason != "" {
		return ScenarioResult{Name: name, Status: StatusBlocked, Command: cmd, LogPath: logPath, Blocker: fmt.Sprintf("exit=%d %s", code, reason)}
	}
	return ScenarioResult{Name: name, Status: StatusFail, Command: cmd, LogPath: logPath, Error: fmt.Sprintf("exit=%d err=%v stdout=%s stderr=%s", code, err, trim(stdout), trim(stderr))}
}

func classifyAPIBlocker(provider, cmd, logPath, stdout, stderr string, code int, err error) ScenarioResult {
	combined := strings.ToLower(stdout + "\n" + stderr + "\n" + fmt.Sprint(err))
	if isAPIEnvironmentBlocker(combined) {
		return ScenarioResult{Name: "API " + provider + " happy-path", Status: StatusBlocked, Command: cmd, LogPath: logPath, Blocker: fmt.Sprintf("provider=%s exit=%d reason=%s", provider, code, trim(stderr))}
	}
	return ScenarioResult{Name: "API " + provider + " happy-path", Status: StatusFail, Command: cmd, LogPath: logPath, Error: fmt.Sprintf("exit=%d err=%v stdout=%s stderr=%s", code, err, trim(stdout), trim(stderr))}
}

func isAPIEnvironmentBlocker(combined string) bool {
	patterns := []string{
		"401 unauthorized",
		"403 forbidden",
		"unauthorized",
		"forbidden",
		"api key",
		"key not allowed",
		"quota",
		"rate limit",
		"timeout",
		"deadline",
		"model not found",
		"model_not_found",
		"model does not exist",
		"model is not supported",
		"do not have access to the model",
		"does not have access to model",
	}
	for _, pattern := range patterns {
		if strings.Contains(combined, pattern) {
			return true
		}
	}
	return false
}

func trim(value string) string {
	value = strings.TrimSpace(value)
	value = strings.ReplaceAll(value, "\r", " ")
	value = strings.ReplaceAll(value, "\n", " ")
	if len(value) > 240 {
		return value[:240] + "..."
	}
	if value == "" {
		return "<empty>"
	}
	return value
}
