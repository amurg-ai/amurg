package adapter

import (
	"fmt"
	"sync"
)

// Registry maps profile names to their adapter constructors.
type Registry struct {
	mu       sync.RWMutex
	adapters map[string]Adapter
}

// NewRegistry creates an empty adapter registry.
func NewRegistry() *Registry {
	return &Registry{
		adapters: make(map[string]Adapter),
	}
}

// Register adds an adapter for a profile name. Panics on duplicate.
func (r *Registry) Register(profile string, adapter Adapter) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.adapters[profile]; exists {
		panic(fmt.Sprintf("adapter already registered for profile: %s", profile))
	}
	r.adapters[profile] = adapter
}

// Get returns the adapter for a profile name.
func (r *Registry) Get(profile string) (Adapter, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	a, ok := r.adapters[profile]
	if !ok {
		return nil, fmt.Errorf("no adapter registered for profile: %s", profile)
	}
	return a, nil
}

// Profiles returns all registered profile names.
func (r *Registry) Profiles() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.adapters))
	for name := range r.adapters {
		names = append(names, name)
	}
	return names
}

// DefaultRegistry creates a registry with all built-in adapters.
func DefaultRegistry() *Registry {
	r := NewRegistry()
	r.Register("generic-cli", &CLIAdapter{})
	r.Register("generic-job", &JobAdapter{})
	r.Register("generic-http", &HTTPAdapter{})
	r.Register("claude-code", &ClaudeCodeAdapter{})
	r.Register("github-copilot", &GitHubCopilotAdapter{})
	r.Register("codex", &CodexAdapter{})
	r.Register("external", &ExternalAdapter{})
	r.Register("kilo-code", &KiloAdapter{})
	return r
}
