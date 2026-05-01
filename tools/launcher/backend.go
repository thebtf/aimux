// Package main implements the launcher debug tool for aimux executors.
package main

import (
	"fmt"
	"os"
	"time"

	"github.com/thebtf/aimux/pkg/config"
	"github.com/thebtf/aimux/pkg/driver"
	"github.com/thebtf/aimux/pkg/executor"
	"github.com/thebtf/aimux/pkg/executor/api"
	"github.com/thebtf/aimux/pkg/executor/conpty"
	"github.com/thebtf/aimux/pkg/executor/pipe"
	"github.com/thebtf/aimux/pkg/executor/pty"
	"github.com/thebtf/aimux/pkg/resolve"
	"github.com/thebtf/aimux/pkg/types"
)

// buildCLIBackend constructs a CLI ExecutorV2 and resolved SpawnArgs from the
// given parameters. It loads CLI profiles from configDir (the aimux config/
// directory that contains default.yaml and cli.d/), runs binary discovery via
// driver.Registry.Probe() so that SpawnArgs.Command holds the full resolved
// path, then delegates argument resolution to the profile resolver.
//
// If cwd is non-empty it is applied to the returned SpawnArgs after resolution.
//
// executorChoice selects the legacy backend wrapped by the adapter:
//
//	"pipe"   — stdin/stdout pipes (default; deterministic for headless CLIs)
//	"conpty" — Windows pseudo-terminal (Win10 1809+); for TUI-style CLIs
//	"pty"    — POSIX pseudo-terminal (Linux/macOS); for TUI-style CLIs
//	"auto"   — best-available via Selector (ConPTY > PTY > Pipe)
//
// "pipe" is the default because ConPTY/PTY emulate a terminal — many headless
// CLIs (codex --json, gemini -p) detect the TTY and either buffer differently,
// expect a controlling terminal handshake, or hang waiting for input that pipe
// mode delivers via stdin. "auto" reproduces the production aimux selector
// path; use it when validating that a profile works under the same backend the
// MCP server picks.
func buildCLIBackend(configDir, cli, prompt, model, effort, cwd, executorChoice string) (types.ExecutorV2, types.SpawnArgs, error) {
	cfg, err := config.Load(configDir)
	if err != nil {
		return nil, types.SpawnArgs{}, fmt.Errorf("load config from %q: %w", configDir, err)
	}

	// Probe discovers binary paths (sets profile.ResolvedPath).
	// Without Probe the resolver falls back to the base binary name and the
	// process spawn may fail on machines where the binary is not in PATH.
	reg := driver.NewRegistry(cfg.CLIProfiles)
	reg.Probe()

	// Validate the requested CLI is configured.
	if _, err := reg.Get(cli); err != nil {
		return nil, types.SpawnArgs{}, fmt.Errorf("CLI %q: %w", cli, err)
	}

	resolver := resolve.NewProfileResolver(cfg.CLIProfiles)
	spawnArgs, err := resolver.ResolveSpawnArgsWithOpts(cli, prompt, model, effort)
	if err != nil {
		return nil, types.SpawnArgs{}, fmt.Errorf("resolve spawn args for %q: %w", cli, err)
	}

	if cwd != "" {
		spawnArgs.CWD = cwd
	}

	exec, err := buildLegacyExecutor(executorChoice)
	if err != nil {
		return nil, types.SpawnArgs{}, err
	}

	return exec, spawnArgs, nil
}

// buildLegacyExecutor constructs a CLI ExecutorV2 for the requested backend
// choice. Returns an error when the requested backend is not available on the
// current platform (e.g., "pty" on Windows, "conpty" on Linux).
func buildLegacyExecutor(choice string) (types.ExecutorV2, error) {
	switch choice {
	case "pipe":
		return executor.NewCLIPipeAdapter(pipe.New()), nil
	case "conpty":
		c := conpty.New()
		if !c.Available() {
			return nil, fmt.Errorf("conpty backend not available on this platform")
		}
		return executor.NewCLIConPTYAdapter(c), nil
	case "pty":
		p := pty.New()
		if !p.Available() {
			return nil, fmt.Errorf("pty backend not available on this platform")
		}
		return executor.NewCLIPTYAdapter(p), nil
	case "auto":
		exec := executor.NewSelector(conpty.New(), pty.New(), pipe.New()).SelectV2()
		if exec == nil {
			return nil, fmt.Errorf("no executor available on this platform")
		}
		return exec, nil
	default:
		return nil, fmt.Errorf("unknown executor choice %q (want pipe|conpty|pty|auto)", choice)
	}
}

// spawnArgsToMetadata converts a SpawnArgs into the Metadata map that
// messageToSpawnArgs (pkg/executor/adapter_common.go) understands.
//
// Key mapping (mirrors adapter_common.go messageToSpawnArgs):
//
//	"command"            → SpawnArgs.Command
//	"args"               → SpawnArgs.Args
//	"cwd"                → SpawnArgs.CWD
//	"stdin"              → SpawnArgs.Stdin  (prompt text)
//	"timeout"            → SpawnArgs.TimeoutSeconds (int, seconds)
//	"completion_pattern" → SpawnArgs.CompletionPattern
//	"env"                → SpawnArgs.Env
//
// Note: adapter_common.go reads "timeout" as int/int64/float64 seconds, so we
// store TimeoutSeconds directly as int. EnvList is a pre-built []string used by
// the resolver when it calls resolve.BuildEnv; it is not a recognized Metadata
// key in adapter_common.go, so we fall back to the Env map for the adapter path.
func spawnArgsToMetadata(args types.SpawnArgs) map[string]any {
	meta := make(map[string]any, 6)

	if args.Command != "" {
		meta["command"] = args.Command
	}
	if len(args.Args) > 0 {
		meta["args"] = args.Args
	}
	if args.CWD != "" {
		meta["cwd"] = args.CWD
	}
	if args.Stdin != "" {
		meta["stdin"] = args.Stdin
	}
	if args.TimeoutSeconds > 0 {
		meta["timeout"] = args.TimeoutSeconds
	}
	if args.CompletionPattern != "" {
		meta["completion_pattern"] = args.CompletionPattern
	}
	if len(args.Env) > 0 {
		meta["env"] = args.Env
	}

	return meta
}

// defaultTimeout is the per-request timeout applied when SpawnArgs carries no
// explicit TimeoutSeconds. Five minutes matches api/executor.go DefaultTimeout.
const defaultTimeout = 5 * time.Minute

// defaultAPIKeyEnv returns the default environment variable name for the API
// key of the given provider. Returns an empty string for unknown providers.
func defaultAPIKeyEnv(provider string) string {
	switch provider {
	case "openai":
		return "OPENAI_API_KEY"
	case "anthropic":
		return "ANTHROPIC_API_KEY"
	case "google":
		return "GOOGLE_AI_API_KEY"
	default:
		return ""
	}
}

// buildAPIBackend constructs an HTTP API ExecutorV2 for the requested provider.
//
// provider must be one of "openai", "anthropic", or "google". model selects
// the specific model; when empty the provider's default model constant is used.
// apiKeyEnv is the name of the environment variable holding the API key; the
// caller must supply the variable name (runAPI defaults it per provider).
//
// HTTP middleware decision (T014):
//
// OpenAI SDK (github.com/openai/openai-go/v3) accepts option.WithHTTPClient.
// Anthropic SDK (github.com/anthropics/anthropic-sdk-go) accepts option.WithHTTPClient.
// Google AI SDK (google.golang.org/genai) uses genai.ClientConfig struct which has
// no HTTPClient field and no functional-options pattern — custom HTTP transport is
// not supported by this SDK's public API (checked at tools/launcher implementation
// time against genai.ClientConfig definition).
//
// Because the Google AI SDK does NOT support custom HTTP client injection, HTTP
// middleware is skipped for ALL providers (Path B) to keep per-provider behaviour
// consistent. A one-line notice is printed to stderr at backend init time.
// The httpRequestPayload / httpResponsePayload structs are defined below in this file
// for future use when Google AI adds HTTPClient support (or is replaced).
func buildAPIBackend(provider, model, apiKeyEnv string) (types.ExecutorV2, error) {
	// Validate provider first so that an unknown provider returns a named error
	// before we attempt to read the API key env var. This ensures the anti-stub
	// contract: bad provider → "unknown provider"; missing key → "env var X empty".
	switch provider {
	case "openai", "anthropic", "google":
		// valid — proceed to key check below
	default:
		return nil, fmt.Errorf("unknown provider %q", provider)
	}

	apiKey := os.Getenv(apiKeyEnv)
	if apiKey == "" {
		return nil, fmt.Errorf("env var %s empty", apiKeyEnv)
	}

	// Emit one-line notice to stderr that HTTP middleware is not active.
	// This runs once per backend construction so the operator knows why
	// http_request / http_response events will not appear in the JSONL log.
	fmt.Fprintf(os.Stderr, "http_middleware: skipped — google.golang.org/genai has no WithHTTPClient option\n")

	switch provider {
	case "openai":
		if model == "" {
			model = api.DefaultOpenAIModel
		}
		return api.NewOpenAI(apiKey, model)
	case "anthropic":
		if model == "" {
			model = api.DefaultAnthropicModel
		}
		return api.NewAnthropic(apiKey, model)
	default: // "google"
		if model == "" {
			model = api.DefaultGoogleAIModel
		}
		return api.NewGoogleAI(apiKey, model)
	}
}

// httpRequestPayload and httpResponsePayload are defined for future use (T014 Path B).
// They are NOT currently emitted: google.golang.org/genai lacks a WithHTTPClient option,
// making uniform HTTP middleware across all three providers impossible. These structs will
// be wired when all active providers support custom HTTP transport injection.

// httpRequestPayload records an outbound HTTP request. Kind: KindHTTPRequest.
type httpRequestPayload struct {
	Method          string            `json:"method"`
	URL             string            `json:"url"`
	BodyPreview     string            `json:"body_preview,omitempty"`
	HeadersRedacted map[string]string `json:"headers_redacted,omitempty"`
}

// httpResponsePayload records an inbound HTTP response. Kind: KindHTTPResponse.
type httpResponsePayload struct {
	Status      string `json:"status"`
	BodyPreview string `json:"body_preview,omitempty"`
	LatencyMs   int64  `json:"latency_ms"`
}
