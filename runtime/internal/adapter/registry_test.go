package adapter

import (
	"context"
	"sort"
	"testing"

	"github.com/amurg-ai/amurg/runtime/internal/config"
)

// dummyAdapter implements Adapter for testing.
type dummyAdapter struct{}

func (d *dummyAdapter) Start(_ context.Context, _ config.AgentConfig) (AgentSession, error) {
	return nil, nil
}

func TestNewRegistry(t *testing.T) {
	r := NewRegistry()
	if r == nil {
		t.Fatal("NewRegistry returned nil")
	}
	if len(r.Profiles()) != 0 {
		t.Errorf("expected empty registry, got %d profiles", len(r.Profiles()))
	}
}

func TestRegistry_Register_And_Get(t *testing.T) {
	r := NewRegistry()
	adp := &dummyAdapter{}

	r.Register("test-profile", adp)

	got, err := r.Get("test-profile")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != adp {
		t.Error("Get returned different adapter than registered")
	}
}

func TestRegistry_Get_NotFound(t *testing.T) {
	r := NewRegistry()

	_, err := r.Get("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent profile, got nil")
	}
}

func TestRegistry_Register_Duplicate_Panics(t *testing.T) {
	r := NewRegistry()
	adp := &dummyAdapter{}

	r.Register("dup-profile", adp)

	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on duplicate registration")
		}
	}()

	r.Register("dup-profile", adp)
}

func TestRegistry_Profiles(t *testing.T) {
	r := NewRegistry()
	r.Register("alpha", &dummyAdapter{})
	r.Register("beta", &dummyAdapter{})
	r.Register("gamma", &dummyAdapter{})

	profiles := r.Profiles()
	sort.Strings(profiles)

	expected := []string{"alpha", "beta", "gamma"}
	if len(profiles) != len(expected) {
		t.Fatalf("expected %d profiles, got %d", len(expected), len(profiles))
	}
	for i, name := range expected {
		if profiles[i] != name {
			t.Errorf("expected profile %s at index %d, got %s", name, i, profiles[i])
		}
	}
}

func TestDefaultRegistry(t *testing.T) {
	r := DefaultRegistry()

	expectedProfiles := []string{
		"generic-cli",
		"generic-job",
		"generic-http",
		"claude-code",
		"github-copilot",
		"codex",
		"kilo-code",
		"external",
	}

	profiles := r.Profiles()
	if len(profiles) != len(expectedProfiles) {
		t.Fatalf("expected %d profiles in default registry, got %d: %v", len(expectedProfiles), len(profiles), profiles)
	}

	for _, name := range expectedProfiles {
		_, err := r.Get(name)
		if err != nil {
			t.Errorf("expected profile %q in default registry: %v", name, err)
		}
	}
}
