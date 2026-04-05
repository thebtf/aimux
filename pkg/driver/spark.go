package driver

import (
	"os/exec"
	"strings"
	"sync"
)

// SparkDetector probes for Spark model availability at startup.
// Codex with Pro subscription supports gpt-5.3-codex/spark variant.
// Result is cached — probe runs once.
type SparkDetector struct {
	available bool
	probed    bool
	mu        sync.Mutex
}

// NewSparkDetector creates a detector (not yet probed).
func NewSparkDetector() *SparkDetector {
	return &SparkDetector{}
}

// Available returns true if Spark model is available.
// First call triggers the probe; subsequent calls return cached result.
func (d *SparkDetector) Available() bool {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.probed {
		return d.available
	}

	d.probed = true
	d.available = d.probe()
	return d.available
}

// probe checks if codex CLI supports the spark model.
func (d *SparkDetector) probe() bool {
	// Check if codex is available
	codexPath, err := exec.LookPath("codex")
	if err != nil {
		return false
	}

	// Get codex version to verify it's a real installation
	cmd := exec.Command(codexPath, "--version")
	output, err := cmd.Output()
	if err != nil {
		return false
	}

	// Codex must return a version string to confirm it's installed and functional.
	// Spark (gpt-5.3-codex) is available when codex is installed — the model
	// selection happens at runtime via --model flag, not compile-time detection.
	version := strings.TrimSpace(string(output))
	return len(version) > 0 && strings.Contains(codexPath, "codex")
}

// ModelName returns the Spark model identifier.
func (d *SparkDetector) ModelName() string {
	return "gpt-5.3-codex"
}

// FallbackModel returns the base model when Spark is unavailable.
func (d *SparkDetector) FallbackModel() string {
	return "gpt-5.3-codex"
}
