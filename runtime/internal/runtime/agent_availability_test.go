package runtime

import (
	"strings"
	"testing"

	"github.com/amurg-ai/amurg/pkg/protocol"
	"github.com/amurg-ai/amurg/runtime/internal/config"
)

func TestWithAgentAvailability_MarksMissingCommandUnavailable(t *testing.T) {
	agent := config.AgentConfig{
		ID:      "claude-1",
		Profile: protocol.ProfileClaudeCode,
		Name:    "Claude",
		ClaudeCode: &config.ClaudeCodeConfig{
			Command: "definitely-not-a-real-command",
		},
	}

	caps := withAgentAvailability(agent, protocol.KnownProfiles[protocol.ProfileClaudeCode])
	if caps.Available {
		t.Fatal("expected agent to be unavailable")
	}
	if !strings.Contains(caps.UnavailableReason, "not on PATH") {
		t.Fatalf("unexpected unavailable reason: %q", caps.UnavailableReason)
	}
}

func TestWithAgentAvailability_MarksMissingWorkDirUnavailable(t *testing.T) {
	agent := config.AgentConfig{
		ID:      "job-1",
		Profile: protocol.ProfileGenericJob,
		Name:    "Job",
		Job: &config.JobConfig{
			Command: "sh",
			WorkDir: "/definitely/missing/path",
		},
	}

	caps := withAgentAvailability(agent, protocol.KnownProfiles[protocol.ProfileGenericJob])
	if caps.Available {
		t.Fatal("expected agent to be unavailable")
	}
	if !strings.Contains(caps.UnavailableReason, "work_dir") {
		t.Fatalf("unexpected unavailable reason: %q", caps.UnavailableReason)
	}
}

func TestWithAgentAvailability_LeavesValidJobAvailable(t *testing.T) {
	agent := config.AgentConfig{
		ID:      "job-1",
		Profile: protocol.ProfileGenericJob,
		Name:    "Job",
		Job: &config.JobConfig{
			Command: "sh",
			WorkDir: t.TempDir(),
		},
	}

	caps := withAgentAvailability(agent, protocol.KnownProfiles[protocol.ProfileGenericJob])
	if !caps.Available {
		t.Fatalf("expected agent to be available, got reason %q", caps.UnavailableReason)
	}
	if caps.UnavailableReason != "" {
		t.Fatalf("expected empty unavailable reason, got %q", caps.UnavailableReason)
	}
}
