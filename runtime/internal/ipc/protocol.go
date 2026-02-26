package ipc

import (
	"encoding/json"
	"time"
)

// Request is a JSON-Lines request from a TUI client.
type Request struct {
	ID     string          `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

// Response is sent back to the client.
type Response struct {
	ID   string          `json:"id,omitempty"`
	Type string          `json:"type"`          // "result" or "error" or "event"
	Data json.RawMessage `json:"data,omitempty"`
}

// StatusResult is returned by the "status" method.
type StatusResult struct {
	RuntimeID    string    `json:"runtime_id"`
	HubURL       string    `json:"hub_url"`
	HubConnected bool      `json:"hub_connected"`
	Reconnecting bool      `json:"reconnecting"`
	Uptime       string    `json:"uptime"`
	StartedAt    time.Time `json:"started_at"`
	Sessions     int       `json:"sessions"`
	MaxSessions  int       `json:"max_sessions"`
	Agents       int       `json:"agents"`
	Version      string    `json:"version"`
}

// SessionInfo describes a single active session.
type SessionInfo struct {
	ID        string    `json:"id"`
	AgentID   string    `json:"agent_id"`
	AgentName string    `json:"agent_name,omitempty"`
	UserID    string    `json:"user_id"`
	State     string    `json:"state"`
	CreatedAt time.Time `json:"created_at"`
}

// SessionsResult is returned by the "sessions" method.
type SessionsResult struct {
	Sessions []SessionInfo `json:"sessions"`
}

// SubscribeParams are sent with the "subscribe" method.
type SubscribeParams struct {
	Events []string `json:"events"`
}

// Event wraps an event bus event for IPC transport.
type Event struct {
	Type      string          `json:"type"`
	Timestamp time.Time       `json:"ts"`
	Data      json.RawMessage `json:"data,omitempty"`
}

// StateProvider is the interface the IPC server uses to query runtime state.
type StateProvider interface {
	Status() StatusResult
	Sessions() []SessionInfo
}
