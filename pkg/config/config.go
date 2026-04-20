// Package config handles YAML configuration loading and CLI profile discovery.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/thebtf/aimux/pkg/types"
	"gopkg.in/yaml.v3"
)

// ServerConfig holds all server-level configuration.
type ServerConfig struct {
	LogLevel                    string `yaml:"log_level"`
	LogFile                     string `yaml:"log_file"`
	DBPath                      string `yaml:"db_path"`
	MaxConcurrentJobs           int    `yaml:"max_concurrent_jobs"`
	MaxPromptBytes              int    `yaml:"max_prompt_bytes"`
	SessionTTLHours             int    `yaml:"session_ttl_hours"`
	GCIntervalSeconds           int    `yaml:"gc_interval_seconds"`
	ProgressIntervalSeconds     int    `yaml:"progress_interval_seconds"`
	StreamingGraceSeconds       int    `yaml:"streaming_grace_seconds"`
	StreamingSoftWarningSeconds int    `yaml:"streaming_soft_warning_seconds"`
	StreamingHardStallSeconds   int    `yaml:"streaming_hard_stall_seconds"`
	StreamingAutoCancelSeconds  int    `yaml:"streaming_auto_cancel_seconds"`
	DefaultAsync                bool   `yaml:"default_async"`
	DefaultTimeoutSeconds       int    `yaml:"default_timeout_seconds"`

	// CLIPriority is the operator-configured tiebreak order for CLI selection.
	// When multiple CLIs can serve a role, the first match in this list wins.
	// CLIs absent from this list are appended after in stable load order.
	// YAML key: cli_priority
	CLIPriority []string `yaml:"cli_priority"`

	// WarmupEnabled controls whether CLI warmup probes run at daemon startup.
	// Set AIMUX_WARMUP=false to skip all probes (binary-only detection).
	WarmupEnabled bool `yaml:"warmup_enabled"`

	// WarmupTimeoutSeconds is the global per-CLI warmup probe timeout.
	// Per-profile warmup_timeout_seconds overrides this for individual CLIs.
	WarmupTimeoutSeconds int `yaml:"warmup_timeout_seconds"`

	RateLimitRPS   float64 `yaml:"rate_limit_rps"`
	RateLimitBurst int     `yaml:"rate_limit_burst"`
	// AuthToken is the bearer token for HTTP/SSE transport authentication.
	// Prefer setting AIMUX_AUTH_TOKEN environment variable — env var takes precedence
	// over this field. If this field is non-empty at startup, a warning is logged.
	AuthToken string `yaml:"auth_token"`

	Transport TransportConfig `yaml:"transport"`
	Audit     AuditConfig     `yaml:"audit"`
	Pair      PairConfig      `yaml:"pair"`
	Consensus ConsensusConfig `yaml:"consensus"`
	Debate    DebateConfig    `yaml:"debate"`
	Research  ResearchConfig  `yaml:"research"`
	Think     ThinkConfig     `yaml:"think"`
}

// AuditConfig holds audit pipeline settings.
type AuditConfig struct {
	ScannerRole             string `yaml:"scanner_role"`
	ValidatorRole           string `yaml:"validator_role"`
	DefaultMode             string `yaml:"default_mode"`
	ParallelScanners        int    `yaml:"parallel_scanners"`
	ScannerTimeoutSeconds   int    `yaml:"scanner_timeout_seconds"`
	ValidatorTimeoutSeconds int    `yaml:"validator_timeout_seconds"`
}

// PairConfig holds pair coding settings.
type PairConfig struct {
	DriverRole             string `yaml:"driver_role"`
	ReviewerRole           string `yaml:"reviewer_role"`
	MaxRounds              int    `yaml:"max_rounds"`
	DriverTimeoutSeconds   int    `yaml:"driver_timeout_seconds"`
	ReviewerTimeoutSeconds int    `yaml:"reviewer_timeout_seconds"`
}

// ConsensusConfig holds consensus tool settings.
type ConsensusConfig struct {
	DefaultBlinded        bool `yaml:"default_blinded"`
	DefaultSynthesize     bool `yaml:"default_synthesize"`
	MaxTurns              int  `yaml:"max_turns"`
	TimeoutPerTurnSeconds int  `yaml:"timeout_per_turn_seconds"`
}

// DebateConfig holds debate tool settings.
type DebateConfig struct {
	DefaultSynthesize     bool `yaml:"default_synthesize"`
	MaxTurns              int  `yaml:"max_turns"`
	TimeoutPerTurnSeconds int  `yaml:"timeout_per_turn_seconds"`
}

// ResearchConfig holds research tool settings.
type ResearchConfig struct {
	DefaultSynthesize            bool `yaml:"default_synthesize"`
	TimeoutPerParticipantSeconds int  `yaml:"timeout_per_participant_seconds"`
}

// ThinkConfig holds think tool settings.
type ThinkConfig struct {
	AutoConsensusThreshold int `yaml:"auto_consensus_threshold"`
	DefaultDialogMaxTurns  int `yaml:"default_dialog_max_turns"`
}

// TransportConfig holds transport selection settings.
type TransportConfig struct {
	Type     string `yaml:"type"`     // "stdio" (default), "sse", "http"
	Port     string `yaml:"port"`     // ":8080" for SSE/HTTP
	Endpoint string `yaml:"endpoint"` // "/mcp" for HTTP
	TLSCert  string `yaml:"tls_cert"` // Path to TLS certificate
	TLSKey   string `yaml:"tls_key"`  // Path to TLS key
}

// CircuitBreakerConfig holds circuit breaker settings.
type CircuitBreakerConfig struct {
	FailureThreshold int `yaml:"failure_threshold"`
	CooldownSeconds  int `yaml:"cooldown_seconds"`
	HalfOpenMaxCalls int `yaml:"half_open_max_calls"`
}

// Config is the root configuration structure.
type Config struct {
	Server         ServerConfig                    `yaml:"server"`
	Roles          map[string]types.RolePreference `yaml:"roles"`
	CircuitBreaker CircuitBreakerConfig            `yaml:"circuit_breaker"`
	CLIProfiles    map[string]*CLIProfile          `yaml:"-"` // loaded from cli.d/
	ConfigDir      string                          `yaml:"-"` // directory containing config files
}

// CLIProfile represents a single CLI plugin configuration.
type CLIProfile struct {
	Name              string            `yaml:"name"`
	Binary            string            `yaml:"binary"`
	DisplayName       string            `yaml:"display_name"`
	Features          types.CLIFeatures `yaml:"features"`
	OutputFormat      string            `yaml:"output_format"`
	Command           CommandConfig     `yaml:"command"`
	PromptFlag        string            `yaml:"prompt_flag"`
	PromptFlagType    string            `yaml:"prompt_flag_type"`
	DefaultModel      string            `yaml:"default_model"`
	ModelFlag         string            `yaml:"model_flag"`
	Reasoning         *ReasoningConfig  `yaml:"reasoning,omitempty"`
	TimeoutSeconds    int               `yaml:"timeout_seconds"`
	StdinThreshold    int               `yaml:"stdin_threshold"`
	// StdinSentinel is an optional argument appended to args when the prompt is
	// delivered via stdin. Codex CLI requires "-" to signal stdin reading.
	// Leave empty for CLIs that read stdin implicitly (Gemini, Aider, etc.).
	StdinSentinel     string            `yaml:"stdin_sentinel,omitempty"`
	CompletionPattern string            `yaml:"completion_pattern,omitempty"`
	ReadOnlyFlags     []string          `yaml:"read_only_flags"`
	HeadlessFlags     []string          `yaml:"headless_flags,omitempty"`
	SearchPaths       []string          `yaml:"search_paths,omitempty"`

	// Capabilities lists what task types this CLI supports (e.g., coding, review, analysis).
	// Used by the fallback router to find a capable substitute when the primary CLI fails.
	Capabilities []string `yaml:"capabilities,omitempty"`

	// ModelFallback is an ordered list of models to try when a rate limit (quota error)
	// is hit. The first entry is the primary model. On quota error, the next model in
	// the chain is tried. Empty = no model fallback (current behavior).
	// This is a profile-internal concern — role-config does not know about it.
	ModelFallback []string `yaml:"model_fallback,omitempty"`

	// FallbackSuffixStrip defines suffixes to strip from the current model name
	// to generate fallback models dynamically. E.g., ["-spark"] means if the active
	// model is "gpt-5.3-codex-spark", the fallback is "gpt-5.3-codex".
	// This survives model version upgrades — no hardcoded model names needed.
	// Applied AFTER ModelFallback is exhausted (or when ModelFallback is empty).
	FallbackSuffixStrip []string `yaml:"fallback_suffix_strip,omitempty"`

	// CooldownSeconds is how long a rate-limited model stays on cooldown before
	// being retried. Only quota errors trigger cooldown (not transient/fatal).
	// Default: 300 (5 minutes).
	CooldownSeconds int `yaml:"cooldown_seconds,omitempty"`

	// WarmupTimeoutSeconds is the per-profile warmup probe timeout override.
	// Zero means use global ServerConfig.WarmupTimeoutSeconds.
	// YAML key: warmup_timeout_seconds
	WarmupTimeoutSeconds int `yaml:"warmup_timeout_seconds,omitempty"`

	// WarmupProbePrompt is the probe prompt sent during warmup.
	// Empty means use the global default: `reply with JSON: {"ok": true}`.
	// YAML key: warmup_probe_prompt
	WarmupProbePrompt string `yaml:"warmup_probe_prompt,omitempty"`

	// EnvPassthrough is the explicit allowlist of environment variable names that this CLI
	// is permitted to inherit from the parent process environment. Any parent env var
	// NOT in this list (and not in the OS-essential baseline) is dropped by resolve.BuildEnv.
	// Set in profile.yaml under env_passthrough:. Do not include secrets — those are
	// injected via ProjectContext.Env at spawn time.
	EnvPassthrough []string `yaml:"env_passthrough,omitempty"`

	// RequiresTTY declares that this CLI requires a real TTY (pseudo-console)
	// to operate correctly. When true and ConPTY is unavailable on the host,
	// buildFallbackCandidates skips this CLI to prevent "No Windows console found"
	// errors from prompt_toolkit-based tools (aider, gptme, qwen).
	// YAML key: requires_tty — default false (absent = false).
	RequiresTTY bool `yaml:"requires_tty,omitempty"`

	// ResolvedPath is set at runtime by discovery — full path to the binary.
	// Not serialized to YAML. Used by executor when binary is not in PATH.
	ResolvedPath string `yaml:"-" json:"resolved_path,omitempty"`
}

// CommandConfig holds command template configuration.
type CommandConfig struct {
	Base         string `yaml:"base"`
	ArgsTemplate string `yaml:"args_template"`
}

// ReasoningConfig holds reasoning effort configuration.
type ReasoningConfig struct {
	Flag              string   `yaml:"flag"`
	FlagValueTemplate string   `yaml:"flag_value_template,omitempty"`
	Levels            []string `yaml:"levels"`
}

// Load reads the main config file and discovers CLI profiles from cli.d/.
func Load(configDir string) (*Config, error) {
	cfg := &Config{
		CLIProfiles: make(map[string]*CLIProfile),
		ConfigDir:   configDir,
		Server: ServerConfig{
			// WarmupEnabled defaults to true — YAML field absent means warmup runs.
			// Set warmup_enabled: false in default.yaml (or AIMUX_WARMUP=false env var)
			// to skip all probes at startup.
			WarmupEnabled: true,
		},
	}

	// Load main config
	mainPath := filepath.Join(configDir, "default.yaml")
	data, err := os.ReadFile(mainPath)
	if err != nil {
		return nil, types.NewConfigError(fmt.Sprintf("failed to read config %s", mainPath), err)
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, types.NewConfigError(fmt.Sprintf("failed to parse config %s", mainPath), err)
	}

	// Discover CLI profiles from cli.d/
	cliDir := filepath.Join(configDir, "cli.d")
	entries, err := os.ReadDir(cliDir)
	if err != nil {
		return nil, types.NewConfigError(fmt.Sprintf("failed to read cli.d/ at %s", cliDir), err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		profilePath := filepath.Join(cliDir, entry.Name(), "profile.yaml")
		profile, err := loadCLIProfile(profilePath)
		if err != nil {
			return nil, types.NewConfigError(fmt.Sprintf("failed to load CLI profile %s", entry.Name()), err)
		}

		cfg.CLIProfiles[profile.Name] = profile
	}

	applyDefaults(cfg)
	return cfg, nil
}

// loadCLIProfile reads a single CLI profile YAML file.
func loadCLIProfile(path string) (*CLIProfile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	var profile CLIProfile
	if err := yaml.Unmarshal(data, &profile); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	if profile.Name == "" {
		return nil, fmt.Errorf("profile %s: name is required", path)
	}

	return &profile, nil
}

// applyDefaults fills in zero-value fields with sensible defaults.
func applyDefaults(cfg *Config) {
	s := &cfg.Server
	if s.LogLevel == "" {
		s.LogLevel = "info"
	}
	if s.MaxConcurrentJobs == 0 {
		s.MaxConcurrentJobs = 10
	}
	if s.MaxPromptBytes == 0 {
		s.MaxPromptBytes = 1048576 // 1MB
	}
	if s.SessionTTLHours == 0 {
		s.SessionTTLHours = 24
	}
	if s.GCIntervalSeconds == 0 {
		s.GCIntervalSeconds = 300
	}
	if s.ProgressIntervalSeconds == 0 {
		s.ProgressIntervalSeconds = 15
	}
	if s.DefaultTimeoutSeconds == 0 {
		s.DefaultTimeoutSeconds = 300
	}
	if s.StreamingGraceSeconds == 0 {
		s.StreamingGraceSeconds = 60
	}
	if s.StreamingSoftWarningSeconds == 0 {
		s.StreamingSoftWarningSeconds = 120
	}
	if s.StreamingHardStallSeconds == 0 {
		s.StreamingHardStallSeconds = 600
	}
	if s.StreamingAutoCancelSeconds == 0 {
		s.StreamingAutoCancelSeconds = 900
	}

	// Warmup defaults: WarmupEnabled is pre-initialized to true in Load() so that
	// configs that omit warmup_enabled still run probes. Only an explicit
	// warmup_enabled: false in YAML (or AIMUX_WARMUP=false env var) disables warmup.
	// No override needed here — preserve the YAML value as-is.
	if s.WarmupTimeoutSeconds == 0 {
		s.WarmupTimeoutSeconds = 15
	}

	cb := &cfg.CircuitBreaker
	if cb.FailureThreshold == 0 {
		cb.FailureThreshold = 3
	}
	if cb.CooldownSeconds == 0 {
		cb.CooldownSeconds = 300
	}
	if cb.HalfOpenMaxCalls == 0 {
		cb.HalfOpenMaxCalls = 1
	}
}

// ExpandPath replaces ~ with the user's home directory.
func ExpandPath(path string) string {
	if strings.HasPrefix(path, "~/") || strings.HasPrefix(path, "~\\") {
		home, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		return filepath.Join(home, path[2:])
	}
	return path
}
