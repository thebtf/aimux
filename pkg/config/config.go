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
	LogLevel              string `yaml:"log_level"`
	LogFile               string `yaml:"log_file"`
	DBPath                string `yaml:"db_path"`
	MaxConcurrentJobs     int    `yaml:"max_concurrent_jobs"`
	MaxPromptBytes        int    `yaml:"max_prompt_bytes"`
	SessionTTLHours       int    `yaml:"session_ttl_hours"`
	GCIntervalSeconds     int    `yaml:"gc_interval_seconds"`
	ProgressIntervalSeconds int  `yaml:"progress_interval_seconds"`
	DefaultAsync          bool   `yaml:"default_async"`
	DefaultTimeoutSeconds int    `yaml:"default_timeout_seconds"`

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
	ScannerRole            string `yaml:"scanner_role"`
	ValidatorRole          string `yaml:"validator_role"`
	DefaultMode            string `yaml:"default_mode"`
	ParallelScanners       int    `yaml:"parallel_scanners"`
	ScannerTimeoutSeconds  int    `yaml:"scanner_timeout_seconds"`
	ValidatorTimeoutSeconds int   `yaml:"validator_timeout_seconds"`
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
	DefaultBlinded         bool `yaml:"default_blinded"`
	DefaultSynthesize      bool `yaml:"default_synthesize"`
	MaxTurns               int  `yaml:"max_turns"`
	TimeoutPerTurnSeconds  int  `yaml:"timeout_per_turn_seconds"`
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
	Type     string `yaml:"type"`      // "stdio" (default), "sse", "http"
	Port     string `yaml:"port"`      // ":8080" for SSE/HTTP
	Endpoint string `yaml:"endpoint"`  // "/mcp" for HTTP
	TLSCert  string `yaml:"tls_cert"`  // Path to TLS certificate
	TLSKey   string `yaml:"tls_key"`   // Path to TLS key
}

// CircuitBreakerConfig holds circuit breaker settings.
type CircuitBreakerConfig struct {
	FailureThreshold  int `yaml:"failure_threshold"`
	CooldownSeconds   int `yaml:"cooldown_seconds"`
	HalfOpenMaxCalls  int `yaml:"half_open_max_calls"`
}

// Config is the root configuration structure.
type Config struct {
	Server         ServerConfig                       `yaml:"server"`
	Roles          map[string]types.RolePreference     `yaml:"roles"`
	CircuitBreaker CircuitBreakerConfig                `yaml:"circuit_breaker"`
	CLIProfiles    map[string]*CLIProfile              `yaml:"-"` // loaded from cli.d/
	ConfigDir      string                              `yaml:"-"` // directory containing config files
}

// CLIProfile represents a single CLI plugin configuration.
type CLIProfile struct {
	Name        string             `yaml:"name"`
	Binary      string             `yaml:"binary"`
	DisplayName string             `yaml:"display_name"`
	Features    types.CLIFeatures  `yaml:"features"`
	OutputFormat string            `yaml:"output_format"`
	Command     CommandConfig      `yaml:"command"`
	PromptFlag  string             `yaml:"prompt_flag"`
	PromptFlagType string          `yaml:"prompt_flag_type"`
	DefaultModel string            `yaml:"default_model"`
	ModelFlag   string             `yaml:"model_flag"`
	Reasoning   *ReasoningConfig   `yaml:"reasoning,omitempty"`
	TimeoutSeconds int             `yaml:"timeout_seconds"`
	StdinThreshold int             `yaml:"stdin_threshold"`
	CompletionPattern string       `yaml:"completion_pattern,omitempty"`
	ReadOnlyFlags []string         `yaml:"read_only_flags"`
	HeadlessFlags []string         `yaml:"headless_flags,omitempty"`
	SearchPaths   []string         `yaml:"search_paths,omitempty"`

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
