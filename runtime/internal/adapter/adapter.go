// Package adapter defines the interface that all agent profiles must implement
// and provides a registry for profile-to-adapter mapping.
package adapter

import (
	"context"
	"io"

	"github.com/amurg-ai/amurg/runtime/internal/config"
)

// Output represents a chunk of agent output.
type Output struct {
	Channel      string // "stdout", "stderr", "system"
	Data         []byte
	ExitCode     *int   // non-nil on final output when process exited
	FileName     string // non-empty when this output is a file
	FileMimeType string // MIME type when this output is a file
}

// Adapter is the interface every agent profile must implement.
// It bridges between the runtime session and the native agent mechanism.
type Adapter interface {
	// Start initializes the agent for a new session. It returns an
	// AgentSession that the runtime uses to communicate with the agent.
	Start(ctx context.Context, cfg config.AgentConfig) (AgentSession, error)
}

// AgentSession represents an active agent interaction.
type AgentSession interface {
	// Send delivers user input to the agent.
	Send(ctx context.Context, input []byte) error

	// Output returns a channel that receives agent output chunks.
	// The channel is closed when the agent has no more output for the current turn.
	Output() <-chan Output

	// Wait blocks until the session ends (process exit, response complete, etc.)
	// and returns any error. For interactive sessions, this blocks until
	// Stop is called or the process exits.
	Wait() error

	// Stop requests the agent to stop. Best-effort.
	Stop() error

	// Close releases all resources. Must be called when the session is done.
	Close() error
}

// ExitCoder is an optional interface for agent sessions that report exit codes.
type ExitCoder interface {
	ExitCode() *int
}

// PermissionRequester is an optional interface for agent sessions that can request
// user permission for tool execution.
type PermissionRequester interface {
	SetPermissionHandler(handler func(tool, description, resource string) (approved bool))
}

// FileDeliverer is an optional interface for agent sessions that can receive files.
type FileDeliverer interface {
	DeliverFile(filePath, fileName, mimeType string) error
}

// WriterAdapter is an optional interface for adapters that accept io.Writer
// for output instead of using channels.
type WriterAdapter interface {
	SetOutput(stdout, stderr io.Writer)
}
