// Package driver loads CLI profiles from cli.d/ plugin directories
// and resolves command templates with feature flags.
package driver

import (
	"fmt"
	"sort"
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
// Searches PATH, well-known directories, version managers, and profile search_paths.
// Runs in parallel for fast startup (Constitution NFR-5: <2s).
func (r *Registry) Probe() {
	var wg sync.WaitGroup

	for name, profile := range r.profiles {
		wg.Add(1)
		go func(name string, profile *config.CLIProfile) {
			defer wg.Done()

			resolvedPath := DiscoverBinary(profile.Binary, profile.SearchPaths)
			r.mu.Lock()
			r.available[name] = resolvedPath != ""
			if resolvedPath != "" {
				profile.ResolvedPath = resolvedPath
			}
			r.mu.Unlock()
		}(name, profile)
	}

	wg.Wait()
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

// SetAvailable marks a CLI as available or unavailable in the registry.
// Intended for testing and programmatic warmup updates; normal availability
// is set by Probe() and RunWarmup().
func (r *Registry) SetAvailable(name string, available bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.available[name] = available
}

// AllCLIs returns names of all configured CLIs (enabled and disabled).
// Used to compute the excluded list in refresh-warmup responses.
func (r *Registry) AllCLIs() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]string, 0, len(r.profiles))
	for name := range r.profiles {
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}

// ProbeableCLIs returns names of CLIs whose binary has been resolved by Probe().
// Warmup consults this list — not EnabledCLIs — so that CLIs marked unavailable
// by a previous warmup probe can be retried and re-enabled on success.
func (r *Registry) ProbeableCLIs() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]string, 0, len(r.profiles))
	for name, profile := range r.profiles {
		if profile != nil && profile.ResolvedPath != "" {
			result = append(result, name)
		}
	}
	sort.Strings(result)
	return result
}

