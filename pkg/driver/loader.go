// Package driver loads CLI profiles from cli.d/ plugin directories
// and resolves command templates with feature flags.
package driver

import (
	"fmt"
	"os/exec"
	"sync"

	"github.com/thebtf/aimux/pkg/config"
)

// Registry holds discovered and available CLI profiles.
type Registry struct {
	profiles  map[string]*config.CLIProfile
	available map[string]bool
	mu        sync.RWMutex
}

// NewRegistry creates a registry from loaded CLI profiles.
func NewRegistry(profiles map[string]*config.CLIProfile) *Registry {
	return &Registry{
		profiles:  profiles,
		available: make(map[string]bool),
	}
}

// Probe checks which CLIs are actually installed and available.
// Runs in parallel for fast startup (Constitution NFR-5: <2s).
func (r *Registry) Probe() {
	var wg sync.WaitGroup

	for name, profile := range r.profiles {
		wg.Add(1)
		go func(name string, profile *config.CLIProfile) {
			defer wg.Done()

			_, err := exec.LookPath(profile.Binary)
			r.mu.Lock()
			r.available[name] = err == nil
			r.mu.Unlock()
		}(name, profile)
	}

	wg.Wait()
}

// IsAvailable returns true if a CLI is installed and reachable.
func (r *Registry) IsAvailable(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.available[name]
}

// EnabledCLIs returns names of all available CLIs.
func (r *Registry) EnabledCLIs() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []string
	for name, ok := range r.available {
		if ok {
			result = append(result, name)
		}
	}
	return result
}

// Get returns a CLI profile by name.
func (r *Registry) Get(name string) (*config.CLIProfile, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	profile, ok := r.profiles[name]
	if !ok {
		return nil, fmt.Errorf("CLI %q not configured", name)
	}
	return profile, nil
}

// All returns all configured CLI profiles.
func (r *Registry) All() map[string]*config.CLIProfile {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make(map[string]*config.CLIProfile, len(r.profiles))
	for k, v := range r.profiles {
		result[k] = v
	}
	return result
}
