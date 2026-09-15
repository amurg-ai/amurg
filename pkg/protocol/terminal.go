package protocol

const (
	TypeTerminalAttach = "terminal.attach"
	TypeTerminalInput  = "terminal.input"
	TypeTerminalResize = "terminal.resize"
	TypeTerminalOutput = "terminal.output"
)

// TerminalRequest is an immediate operation, never queued or replayed after a
// disconnect. Data is base64 on JSON transports to preserve arbitrary bytes.
type TerminalRequest struct {
	SessionID  string `json:"session_id"`
	Generation string `json:"generation,omitempty"`
	Data       []byte `json:"data,omitempty"`
	Cols       uint16 `json:"cols,omitempty"`
	Rows       uint16 `json:"rows,omitempty"`
	// Set by the hub after checking ownership, never trusted from the browser.
	AgentID string `json:"agent_id,omitempty"`
	UserID  string `json:"user_id,omitempty"`
}

// TerminalOutput is a live terminal display stream, not a chat message or turn.
// A reset starts a new generation; all viewers reset their emulator before
// applying that generation's output. Reconnect requires a fresh attach.
type TerminalOutput struct {
	SessionID  string `json:"session_id"`
	Generation string `json:"generation"`
	Kind       string `json:"kind"` // reset, output, detached, error
	Data       []byte `json:"data,omitempty"`
	Cols       uint16 `json:"cols,omitempty"`
	Rows       uint16 `json:"rows,omitempty"`
	Error      string `json:"error,omitempty"`
}

// FileReceived confirms that an upload is available on the agent's host.
// The mobile composer keeps it as an attachment until the user submits a prompt.
const TypeFileReceived = "file.received"

type FileReceived struct {
	SessionID string `json:"session_id"`
	FileID    string `json:"file_id"`
	Name      string `json:"name"`
	Path      string `json:"path"`
}
