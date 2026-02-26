// Package protocol defines the wire protocol messages exchanged between
// Amurg components (runtime ↔ hub ↔ UI client) over WebSocket.
//
// All messages are JSON-encoded and share a common envelope with a "type" field
// that determines the payload structure.
package protocol

import "time"

// Envelope is the top-level wire format for all messages.
type Envelope struct {
	Type      string    `json:"type"`
	ID        string    `json:"id,omitempty"`        // message ID for idempotency
	SessionID string    `json:"session_id,omitempty"`
	Timestamp time.Time `json:"ts"`
	Payload   any       `json:"payload,omitempty"`
}

// --- Runtime ↔ Hub messages ---

// RuntimeHello is sent by the runtime immediately after connecting.
type RuntimeHello struct {
	RuntimeID string                 `json:"runtime_id"`
	Token     string                 `json:"token"`
	OrgID     string                 `json:"org_id,omitempty"` // empty defaults to "default"
	Agents []AgentRegistration `json:"agents"`
}

// SecurityProfile defines security constraints for an agent.
type SecurityProfile struct {
	AllowedPaths   []string `json:"allowed_paths,omitempty"`
	DeniedPaths    []string `json:"denied_paths,omitempty"`
	AllowedTools   []string `json:"allowed_tools,omitempty"`
	PermissionMode string   `json:"permission_mode,omitempty"`
	Cwd            string   `json:"cwd,omitempty"`
	EnvWhitelist   []string `json:"env_whitelist,omitempty"`
}

// AgentRegistration describes an agent the runtime offers.
type AgentRegistration struct {
	ID       string            `json:"id"`
	Profile  string            `json:"profile"`
	Name     string            `json:"name"`
	Tags     map[string]string `json:"tags,omitempty"`
	Caps     ProfileCaps       `json:"caps"`
	Security *SecurityProfile  `json:"security,omitempty"`
}

// ProfileCaps declares capabilities for a profile (spec §5.2).
type ProfileCaps struct {
	NativeSessionIDs bool           `json:"native_session_ids"`
	TurnCompletion   bool           `json:"turn_completion"`
	ResumeAttach     bool           `json:"resume_attach"`
	ExecModel        ExecutionModel `json:"exec_model"`
}

// ExecutionModel describes how the agent executes.
type ExecutionModel string

const (
	ExecInteractive      ExecutionModel = "interactive"
	ExecRequestResponse  ExecutionModel = "request-response"
	ExecRunToCompletion  ExecutionModel = "run-to-completion"
)

// HelloAck is the hub's response to RuntimeHello.
type HelloAck struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// --- Session lifecycle ---

// SessionCreate is sent by the hub to the runtime to create a new session.
type SessionCreate struct {
	SessionID  string `json:"session_id"`
	AgentID string `json:"agent_id"`
	UserID  string `json:"user_id"`
}

// SessionCreated is the runtime's response to SessionCreate.
type SessionCreated struct {
	SessionID    string `json:"session_id"`
	OK           bool   `json:"ok"`
	Error        string `json:"error,omitempty"`
	NativeHandle string `json:"native_handle,omitempty"`
}

// SessionClose is sent by either side to close a session.
type SessionClose struct {
	SessionID string `json:"session_id"`
	Reason    string `json:"reason,omitempty"`
}

// --- Message flow ---

// UserMessage carries user input to the agent (hub → runtime).
type UserMessage struct {
	SessionID string `json:"session_id"`
	MessageID string `json:"message_id"` // client-generated UUID for idempotency
	Content   string `json:"content"`
}

// AgentOutput carries agent output back (runtime → hub → UI).
type AgentOutput struct {
	SessionID string `json:"session_id"`
	MessageID string `json:"message_id,omitempty"` // hub-assigned
	Seq       int64  `json:"seq"`                  // monotonic per session
	Channel   string `json:"channel"`              // "stdout", "stderr", "system"
	Content   string `json:"content"`
	Final     bool   `json:"final"` // true if this is the last chunk for this turn
}

// --- Turn management ---

// TurnStarted signals the beginning of an agent response.
type TurnStarted struct {
	SessionID     string `json:"session_id"`
	InResponseTo  string `json:"in_response_to"` // message_id of the user message
}

// TurnCompleted signals the end of an agent response.
type TurnCompleted struct {
	SessionID    string `json:"session_id"`
	InResponseTo string `json:"in_response_to"`
	ExitCode     *int   `json:"exit_code,omitempty"` // for run-to-completion profiles
}

// --- Stop / Cancel ---

// StopRequest is sent by the hub to the runtime.
type StopRequest struct {
	SessionID string `json:"session_id"`
}

// StopAck is the runtime's acknowledgment.
type StopAck struct {
	SessionID string `json:"session_id"`
	OK        bool   `json:"ok"`
	Error     string `json:"error,omitempty"`
}

// --- Heartbeat ---

// Ping/Pong for connection liveness.
type Ping struct{}
type Pong struct{}

// --- Message type constants ---

const (
	// Runtime ↔ Hub
	TypeRuntimeHello  = "runtime.hello"
	TypeHelloAck      = "hello.ack"
	TypeSessionCreate = "session.create"
	TypeSessionCreated = "session.created"
	TypeSessionClose  = "session.close"
	TypeUserMessage   = "user.message"
	TypeAgentOutput   = "agent.output"
	TypeTurnStarted   = "turn.started"
	TypeTurnCompleted = "turn.completed"
	TypeStopRequest   = "stop.request"
	TypeStopAck       = "stop.ack"
	TypePing          = "ping"
	TypePong          = "pong"

	// Token refresh
	TypeRuntimeTokenRefresh = "runtime.token_refresh"

	// Client ↔ Hub (API/WS)
	TypeClientSubscribe   = "client.subscribe"
	TypeClientUnsubscribe = "client.unsubscribe"
	TypeHistoryResponse   = "history.response"
	TypeAgentList         = "agent.list"
	TypeSessionList       = "session.list"
	TypeErrorResponse     = "error"
	TypeSessionClosed     = "session.closed"

	// Permission flow
	TypePermissionRequest  = "permission.request"
	TypePermissionResponse = "permission.response"

	// File transfer (hub ↔ runtime)
	TypeFileUpload    = "file.upload"    // hub → runtime: user uploaded a file
	TypeFileAvailable = "file.available" // runtime → hub: agent produced a file

	// Agent config management (hub → runtime)
	TypeAgentConfigUpdate = "agent.config_update" // hub → runtime: apply config override
	TypeAgentConfigAck    = "agent.config_ack"    // runtime → hub: acknowledge config update
)

// --- Client ↔ Hub messages ---

// ClientSubscribe requests live updates for a session.
type ClientSubscribe struct {
	SessionID string `json:"session_id"`
	AfterSeq  int64  `json:"after_seq"` // resume from this sequence number
}

// ClientUnsubscribe stops live updates for a session.
type ClientUnsubscribe struct {
	SessionID string `json:"session_id"`
}

// HistoryResponse returns stored messages for a session.
type HistoryResponse struct {
	SessionID string        `json:"session_id"`
	Messages  []StoredMessage `json:"messages"`
}

// StoredMessage is a persisted message in the transcript.
type StoredMessage struct {
	ID        string    `json:"id"`
	SessionID string    `json:"session_id"`
	Seq       int64     `json:"seq"`
	Direction string    `json:"direction"` // "user" or "agent"
	Channel   string    `json:"channel"`
	Content   string    `json:"content"`
	Timestamp time.Time `json:"ts"`
}

// AgentInfo describes an agent visible to a user.
type AgentInfo struct {
	ID          string            `json:"id"`
	RuntimeID   string            `json:"runtime_id"`
	RuntimeName string            `json:"runtime_name"`
	Profile     string            `json:"profile"`
	Name        string            `json:"name"`
	Tags        map[string]string `json:"tags,omitempty"`
	Online      bool              `json:"online"`
	Caps        ProfileCaps       `json:"caps"`
	Security    *SecurityProfile  `json:"security,omitempty"`
}

// SessionInfo describes a session visible to a user.
type SessionInfo struct {
	ID         string    `json:"id"`
	AgentID string    `json:"agent_id"`
	Profile string    `json:"profile"`
	State   string    `json:"state"` // "active", "idle", "closed"
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// ErrorResponse carries an error from hub to client.
type ErrorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// RuntimeTokenRefresh carries a new token from hub to runtime.
type RuntimeTokenRefresh struct {
	Token string `json:"token"`
}

// PermissionRequest is sent by a runtime when an agent tool needs user approval.
type PermissionRequest struct {
	SessionID   string `json:"session_id"`
	RequestID   string `json:"request_id"`
	Tool        string `json:"tool"`
	Description string `json:"description"`
	Resource    string `json:"resource,omitempty"`
}

// PermissionResponse carries the user's approval/denial back to the runtime.
type PermissionResponse struct {
	SessionID   string `json:"session_id"`
	RequestID   string `json:"request_id"`
	Approved    bool   `json:"approved"`
	AlwaysAllow bool   `json:"always_allow,omitempty"`
}

// --- Agent config management ---

// AgentConfigUpdate is sent by the hub to the runtime to apply config overrides.
type AgentConfigUpdate struct {
	AgentID  string           `json:"agent_id"`
	Security *SecurityProfile `json:"security,omitempty"`
	Limits   *AgentLimits     `json:"limits,omitempty"`
}

// AgentLimits carries operational limits as wire-friendly strings.
type AgentLimits struct {
	MaxSessions    int    `json:"max_sessions,omitempty"`
	SessionTimeout string `json:"session_timeout,omitempty"` // duration string, e.g. "30m"
	MaxOutputBytes int64  `json:"max_output_bytes,omitempty"`
	IdleTimeout    string `json:"idle_timeout,omitempty"` // duration string, e.g. "5m"
}

// AgentConfigAck is the runtime's acknowledgment of a config update.
type AgentConfigAck struct {
	AgentID string `json:"agent_id"`
	OK      bool   `json:"ok"`
	Error   string `json:"error,omitempty"`
}

// --- File transfer ---

// FileMetadata describes a file being transferred.
type FileMetadata struct {
	FileID   string `json:"file_id"`
	Name     string `json:"name"`
	MimeType string `json:"mime_type"`
	Size     int64  `json:"size"`
}

// FileUpload carries a user-uploaded file from hub to runtime.
type FileUpload struct {
	SessionID string       `json:"session_id"`
	Metadata  FileMetadata `json:"metadata"`
	Data      string       `json:"data"` // base64-encoded file content
}

// FileAvailable carries an agent-produced file from runtime to hub.
type FileAvailable struct {
	SessionID string       `json:"session_id"`
	Metadata  FileMetadata `json:"metadata"`
	Data      string       `json:"data"` // base64-encoded file content
}
