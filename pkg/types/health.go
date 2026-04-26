package types

// HealthStatus represents the health state of an executor managed by Swarm.
type HealthStatus int

const (
	// HealthAlive means the executor is responsive and processing requests normally.
	HealthAlive HealthStatus = iota

	// HealthDegraded means the executor is responsive but showing signs of trouble:
	// slow responses, high error rate, or approaching resource limits.
	HealthDegraded

	// HealthDead means the executor is unresponsive: process exited, connection lost,
	// or health check timed out.
	HealthDead

	// HealthUnknown means the executor's state has not been determined yet
	// (e.g., just spawned, health check not yet run).
	HealthUnknown
)

// String returns the human-readable health status name.
func (h HealthStatus) String() string {
	switch h {
	case HealthAlive:
		return "alive"
	case HealthDegraded:
		return "degraded"
	case HealthDead:
		return "dead"
	case HealthUnknown:
		return "unknown"
	default:
		return "invalid"
	}
}

// IsHealthy returns true if the executor can accept requests (alive or degraded).
func (h HealthStatus) IsHealthy() bool {
	return h == HealthAlive || h == HealthDegraded
}

// ExecutorType distinguishes CLI-based and API-based executors.
type ExecutorType int

const (
	// ExecutorTypeCLI is a CLI binary executor (codex, claude, gemini, aider, etc.).
	ExecutorTypeCLI ExecutorType = iota

	// ExecutorTypeAPI is an HTTP API executor (OpenAI, Anthropic, Google AI).
	ExecutorTypeAPI
)

// String returns the human-readable executor type name.
func (t ExecutorType) String() string {
	switch t {
	case ExecutorTypeCLI:
		return "cli"
	case ExecutorTypeAPI:
		return "api"
	default:
		return "unknown"
	}
}

// ExecutorCapabilities describes what an executor implementation supports.
type ExecutorCapabilities struct {
	// Streaming indicates the executor can deliver incremental output via SendStream.
	Streaming bool `json:"streaming"`

	// Images indicates the executor can accept image inputs.
	Images bool `json:"images"`

	// Tools indicates the executor supports tool/function calling.
	Tools bool `json:"tools"`

	// PersistentSessions indicates the executor can maintain state across multiple
	// Send calls without restarting the underlying process/connection.
	PersistentSessions bool `json:"persistent_sessions"`
}

// ExecutorInfo provides metadata about an executor implementation.
// Returned by ExecutorV2.Info() for capability discovery.
type ExecutorInfo struct {
	// Name is the executor implementation name (e.g., "pipe", "conpty", "openai").
	Name string `json:"name"`

	// Type distinguishes CLI from API executors.
	Type ExecutorType `json:"type"`

	// Capabilities describes what this executor supports.
	Capabilities ExecutorCapabilities `json:"capabilities"`
}
