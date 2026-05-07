package picker

import "time"

// PickerConfig holds all configuration for the CLI picker.
// It is loaded from the `executor.picker` section of config.yaml and
// merged with defaults via DefaultPickerConfig().
//
// All fields are optional; zero values cause Pick to use built-in defaults.
type PickerConfig struct {
	// DefaultCLI is the CLI to use when no task-class-specific preference
	// is set. When non-empty and the CLI is healthy, it wins over the
	// capability score table.
	// YAML key: default_cli
	DefaultCLI string `yaml:"default_cli"`

	// PreferCLI maps task class names to preferred CLI names.
	// When an entry exists for the request's TaskClass and the CLI is healthy,
	// it wins over the capability score table.
	// YAML key: prefer_cli
	PreferCLI map[string]string `yaml:"prefer_cli"`

	// DisabledCLIs is a list of CLI names to exclude from all selection,
	// regardless of health or score. Useful for temporarily removing a CLI
	// without editing profiles.
	// YAML key: disabled_clis
	DisabledCLIs []string `yaml:"disabled_clis"`

	// Scores overrides the default capability score table.
	// Structure: cli_name → task_class → score (0–100).
	// Entries not present here fall back to built-in defaults.
	// YAML key: scores
	Scores map[string]map[string]int `yaml:"scores"`

	// HealthCacheTTL controls how long a health check result is cached.
	// Zero value is replaced with 60s by DefaultPickerConfig.
	// YAML key: health_cache_ttl (parsed as Go duration string)
	HealthCacheTTL time.Duration `yaml:"health_cache_ttl"`
}

// DefaultPickerConfig returns a PickerConfig with sensible defaults.
// Callers who load from YAML should merge their YAML result with this
// to ensure non-zero TTL when the YAML field is absent.
func DefaultPickerConfig() PickerConfig {
	return PickerConfig{
		HealthCacheTTL: 60 * time.Second,
	}
}

// isDisabled returns true if the given CLI name appears in the disabled list.
func (c *PickerConfig) isDisabled(cli string) bool {
	for _, d := range c.DisabledCLIs {
		if d == cli {
			return true
		}
	}
	return false
}
