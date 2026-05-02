package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const largeOutputBytes = 50 * 1024 * 1024

type logEventLite struct {
	Kind    string          `json:"kind"`
	Payload json.RawMessage `json:"payload"`
}

func runSyntheticScenarios(opts validateOptions, launcher, runDir string) []ScenarioResult {
	cfgDir, binDir, err := prepareSyntheticConfig(runDir)
	if err != nil {
		return []ScenarioResult{{Name: "synthetic fixture setup", Status: StatusFail, Error: err.Error()}}
	}
	_ = binDir
	return []ScenarioResult{
		runSyntheticL1Scenario(opts, launcher, cfgDir, runDir),
		runSyntheticANSIScenario(opts, launcher, cfgDir, runDir),
		runLargeOutputScenario(opts, launcher, cfgDir, runDir),
	}
}

func prepareSyntheticConfig(runDir string) (string, string, error) {
	binDir := filepath.Join(runDir, "synthetic-bin")
	cfgDir := filepath.Join(runDir, "synthetic-config")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return "", "", err
	}
	if err := buildEmitter("ansi", filepath.Join(binDir, exeName("synthetic-ansi"))); err != nil {
		return "", "", err
	}
	if err := buildEmitter("large", filepath.Join(binDir, exeName("synthetic-large"))); err != nil {
		return "", "", err
	}
	if err := os.MkdirAll(filepath.Join(cfgDir, "cli.d", "synthetic-ansi"), 0o755); err != nil {
		return "", "", err
	}
	if err := os.MkdirAll(filepath.Join(cfgDir, "cli.d", "synthetic-large"), 0o755); err != nil {
		return "", "", err
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "default.yaml"), []byte("server:\n  default_timeout_seconds: 60\n"), 0o644); err != nil {
		return "", "", err
	}
	profiles := map[string]string{
		"synthetic-ansi":  syntheticProfile("synthetic-ansi", "synthetic-ansi", binDir, ""),
		"synthetic-large": syntheticProfile("synthetic-large", "synthetic-large -mb 51", binDir, ""),
	}
	for name, content := range profiles {
		path := filepath.Join(cfgDir, "cli.d", name, "profile.yaml")
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return "", "", err
		}
	}
	return cfgDir, binDir, nil
}

func buildEmitter(name, out string) error {
	repoRoot, err := findLauncherRepoRoot()
	if err != nil {
		return err
	}
	source := filepath.Join("tools", "launcher", "testdata", "emitters", name)
	cmd := exec.Command("go", "build", "-o", out, "./"+source)
	cmd.Dir = repoRoot
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func syntheticProfile(name, commandBase, binDir, completion string) string {
	return fmt.Sprintf(`name: %s
binary: %s
display_name: %q
capabilities: [analysis]
features:
  streaming: false
  headless: true
  stdin_pipe: true
output_format: text
command:
  base: %q
prompt_flag: ""
prompt_flag_type: "stdin"
timeout_seconds: 60
stdin_threshold: 1
completion_pattern: %q
search_paths:
  - %q
`, name, strings.Fields(commandBase)[0], name, commandBase, completion, binDir)
}

func runSyntheticL1Scenario(opts validateOptions, launcher, cfgDir, runDir string) ScenarioResult {
	logPath := filepath.Join(runDir, "synthetic-l1.jsonl")
	args := []string{"cli", "--cli", "synthetic-ansi", "--config-dir", cfgDir, "--prompt", "ignored", "--log", logPath}
	stdout, stderr, code, err := timedLauncher(opts, launcher, args, "")
	cmd := launcherCommand(args)
	if err != nil || code != 0 {
		return ScenarioResult{Name: "synthetic L1 JSONL", Status: StatusFail, Command: cmd, LogPath: logPath, Error: fmt.Sprintf("exit=%d err=%v stderr=%s", code, err, trim(stderr))}
	}
	missing := missingKinds(logPath, []string{KindSpawnArgs, KindComplete, KindClassify, KindBreakerState, KindCooldownState})
	if len(missing) > 0 {
		return ScenarioResult{Name: "synthetic L1 JSONL", Status: StatusFail, Command: cmd, LogPath: logPath, Error: "missing JSONL kinds: " + strings.Join(missing, ",")}
	}
	return ScenarioResult{Name: "synthetic L1 JSONL", Status: StatusPass, Command: cmd, LogPath: logPath, Evidence: []string{"required L1 event kinds present", "stdout=" + trim(stdout)}}
}

func runSyntheticANSIScenario(opts validateOptions, launcher, cfgDir, runDir string) ScenarioResult {
	logPath := filepath.Join(runDir, "synthetic-ansi-raw.jsonl")
	args := []string{"cli", "--cli", "synthetic-ansi", "--config-dir", cfgDir, "--prompt", "ignored", "--bypass", "--log", logPath}
	_, stderr, code, err := timedLauncher(opts, launcher, args, "")
	cmd := launcherCommand(args)
	if err != nil || code != 0 {
		return ScenarioResult{Name: "synthetic ANSI raw-vs-line", Status: StatusFail, Command: cmd, LogPath: logPath, Error: fmt.Sprintf("exit=%d err=%v stderr=%s", code, err, trim(stderr))}
	}
	rawANSI, stripped, scanErr := inspectANSIProof(logPath)
	if scanErr != nil {
		return ScenarioResult{Name: "synthetic ANSI raw-vs-line", Status: StatusFail, Command: cmd, LogPath: logPath, Error: scanErr.Error()}
	}
	if !rawANSI || !stripped {
		return ScenarioResult{Name: "synthetic ANSI raw-vs-line", Status: StatusFail, Command: cmd, LogPath: logPath, Error: fmt.Sprintf("raw_has_1b5b=%v stripped_line_ok=%v", rawANSI, stripped)}
	}
	return ScenarioResult{Name: "synthetic ANSI raw-vs-line", Status: StatusPass, Command: cmd, LogPath: logPath, Evidence: []string{"raw stdout bytes_hex contains 1b5b", "line stdout contains AIMUX_ANSI_OK without ANSI escapes"}}
}

func runLargeOutputScenario(opts validateOptions, launcher, cfgDir, runDir string) ScenarioResult {
	logPath := filepath.Join(runDir, "synthetic-large-raw.jsonl")
	args := []string{"cli", "--cli", "synthetic-large", "--config-dir", cfgDir, "--prompt", "ignored", "--bypass", "--log", logPath}
	_, stderr, code, memory, err := timedLauncherMeasured(opts, launcher, args, "")
	cmd := launcherCommand(args)
	if err != nil || code != 0 {
		return ScenarioResult{Name: "synthetic large-output raw capture", Status: StatusFail, Command: cmd, LogPath: logPath, Error: fmt.Sprintf("exit=%d err=%v stderr=%s", code, err, trim(stderr))}
	}
	rawBytes, logSize, scanErr := inspectRawByteCount(logPath)
	if scanErr != nil {
		return ScenarioResult{Name: "synthetic large-output raw capture", Status: StatusFail, Command: cmd, LogPath: logPath, Error: scanErr.Error()}
	}
	if rawBytes < largeOutputBytes {
		return ScenarioResult{Name: "synthetic large-output raw capture", Status: StatusFail, Command: cmd, LogPath: logPath, Error: fmt.Sprintf("raw output %d bytes below required %d", rawBytes, largeOutputBytes)}
	}
	if !memory.Available {
		return ScenarioResult{Name: "synthetic large-output raw capture", Status: StatusBlocked, Command: cmd, LogPath: logPath, Blocker: memoryEvidenceUnavailable(), Evidence: []string{fmt.Sprintf("raw output bytes=%d", rawBytes), fmt.Sprintf("JSONL log size bytes=%d", logSize)}}
	}
	return ScenarioResult{Name: "synthetic large-output raw capture", Status: StatusPass, Command: cmd, LogPath: logPath, Evidence: []string{fmt.Sprintf("raw output bytes=%d", rawBytes), fmt.Sprintf("JSONL log size bytes=%d", logSize), memoryEvidence(memory)}}
}

func runRealCLIScenarios(opts validateOptions, launcher, runDir string) []ScenarioResult {
	var results []ScenarioResult
	for _, cli := range opts.CLIScope {
		logPath := filepath.Join(runDir, "real-cli-"+cli+".jsonl")
		args := []string{"cli", "--cli", cli, "--config-dir", opts.ConfigDir, "--prompt", "Respond with exactly AIMUX_OK.", "--log", logPath}
		stdout, stderr, code, err := timedLauncher(opts, launcher, args, "")
		cmd := launcherCommand(args)
		if err != nil || code != 0 {
			results = append(results, classifyExternalBlocker("real CLI "+cli+" one-shot/L1", cmd, logPath, stdout, stderr, code, err))
			continue
		}
		missing := missingKinds(logPath, []string{KindSpawnArgs, KindComplete, KindClassify})
		if len(missing) > 0 {
			results = append(results, ScenarioResult{Name: "real CLI " + cli + " one-shot/L1", Status: StatusFail, Command: cmd, LogPath: logPath, Error: "missing JSONL kinds: " + strings.Join(missing, ",")})
			continue
		}
		results = append(results, ScenarioResult{Name: "real CLI " + cli + " one-shot/L1", Status: StatusPass, Command: cmd, LogPath: logPath, Evidence: []string{"launcher exited 0", "L1 JSONL events present", "stdout=" + trim(stdout)}})
	}
	return results
}

func runAPIScenarios(opts validateOptions, launcher, runDir string) []ScenarioResult {
	providers := map[string]string{"openai": "OPENAI_API_KEY", "anthropic": "ANTHROPIC_API_KEY", "google": "GOOGLE_AI_API_KEY"}
	var results []ScenarioResult
	for _, provider := range sortedKeys(providers) {
		envName := providers[provider]
		logPath := filepath.Join(runDir, "api-"+provider+".jsonl")
		args := []string{"api", "--provider", provider, "--prompt", "Reply with exactly AIMUX_API_OK.", "--api-key-env", envName, "--log", logPath}
		cmd := launcherCommand(args)
		if os.Getenv(envName) == "" {
			results = append(results, ScenarioResult{Name: "API " + provider + " happy-path", Status: StatusBlocked, Command: cmd, LogPath: logPath, Blocker: "missing env var " + envName})
			continue
		}
		stdout, stderr, code, err := timedLauncher(opts, launcher, args, "")
		if err != nil || code != 0 {
			results = append(results, classifyAPIBlocker(provider, cmd, logPath, stdout, stderr, code, err))
			continue
		}
		results = append(results, ScenarioResult{Name: "API " + provider + " happy-path", Status: StatusPass, Command: cmd, LogPath: logPath, Evidence: []string{"launcher exited 0", "stdout=" + trim(stdout)}})
	}
	return results
}

func runSessionScenario(opts validateOptions, launcher, runDir string) ScenarioResult {
	if len(opts.CLIScope) == 0 {
		return ScenarioResult{Name: "stdin-driven CLI session", Status: StatusBlocked, Blocker: "empty --cli-scope"}
	}
	cli := opts.CLIScope[0]
	logPath := filepath.Join(runDir, "session-"+cli+".jsonl")
	args := []string{"session", "--cli", cli, "--config-dir", opts.ConfigDir, "--log", logPath}
	stdin := "Reply with exactly AIMUX_SESSION_OK.\n/dump\n/quit\n"
	stdout, stderr, code, err := timedLauncher(opts, launcher, args, stdin)
	cmd := "printf <session-script> | " + launcherCommand(args)
	if err != nil || code != 0 {
		return classifyExternalBlocker("stdin-driven CLI session", cmd, logPath, stdout, stderr, code, err)
	}
	if !hasTurnRoles(logPath) {
		return ScenarioResult{Name: "stdin-driven CLI session", Status: StatusFail, Command: cmd, LogPath: logPath, Error: "missing user/agent turn events"}
	}
	return ScenarioResult{Name: "stdin-driven CLI session", Status: StatusPass, Command: cmd, LogPath: logPath, Evidence: []string{"turn events include user and agent", "/dump executed before /quit", "stdout=" + trim(stdout)}}
}

func manualTUIRecipe(opts validateOptions, runDir string) []string {
	cli := "gemini"
	if len(opts.CLIScope) > 0 {
		cli = opts.CLIScope[len(opts.CLIScope)-1]
	}
	logPath := filepath.Join(runDir, "manual-tui-"+cli+".jsonl")
	return []string{
		"1. Run: `launcher session --cli " + cli + " --executor conpty --config-dir " + opts.ConfigDir + " --log " + logPath + "` (use `--executor pty` on Unix if preferred).",
		"2. Type a short prompt and confirm the CLI TUI renders in the terminal.",
		"3. Type `/help` and confirm launcher-level TUI commands are visible. `/dump` is intentionally unavailable in interactive TUI mode; the automated stdin-driven session scenario covers `/dump` evidence.",
		"4. Type `/quit` and confirm the process exits cleanly with exit code 0.",
		"5. Artifact checklist: JSONL log path, screenshot or terminal transcript, visible TUI render evidence, automated session `/dump` evidence from this report, clean close evidence.",
	}
}

func timedLauncher(opts validateOptions, launcher string, args []string, stdin string) (string, string, int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), opts.Timeout)
	defer cancel()
	return runLauncher(ctx, launcher, args, stdin)
}

func timedLauncherMeasured(opts validateOptions, launcher string, args []string, stdin string) (string, string, int, processMeasurement, error) {
	ctx, cancel := context.WithTimeout(context.Background(), opts.Timeout)
	defer cancel()
	return runLauncherMeasured(ctx, launcher, args, stdin)
}

func launcherCommand(args []string) string { return "launcher " + strings.Join(args, " ") }

func exeName(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}
