// Package store defines the storage interface for the hub and provides SQLite and PostgreSQL implementations.
package store

import (
	"context"
	"encoding/json"
	"time"
)

// Store is the persistence interface for the hub.
type Store interface {
	// Organizations
	CreateOrganization(ctx context.Context, org *Organization) error
	GetOrganization(ctx context.Context, id string) (*Organization, error)

	// Users
	CreateUser(ctx context.Context, user *User) error
	GetUser(ctx context.Context, orgID, username string) (*User, error)
	GetUserByID(ctx context.Context, id string) (*User, error)
	GetUserByExternalID(ctx context.Context, externalID string) (*User, error)
	ListUsers(ctx context.Context, orgID string) ([]User, error)

	// Runtimes
	UpsertRuntime(ctx context.Context, rt *Runtime) error
	GetRuntime(ctx context.Context, id string) (*Runtime, error)
	ListRuntimes(ctx context.Context, orgID string) ([]Runtime, error)
	SetRuntimeOnline(ctx context.Context, id string, online bool) error

	// Endpoints
	UpsertEndpoint(ctx context.Context, ep *Endpoint) error
	GetEndpoint(ctx context.Context, id string) (*Endpoint, error)
	ListEndpoints(ctx context.Context, orgID string) ([]Endpoint, error)
	ListEndpointsByRuntime(ctx context.Context, runtimeID string) ([]Endpoint, error)
	DeleteEndpointsByRuntime(ctx context.Context, runtimeID string) error

	// Sessions
	CreateSession(ctx context.Context, sess *Session) error
	GetSession(ctx context.Context, id string) (*Session, error)
	ListSessionsByUser(ctx context.Context, userID string) ([]Session, error)
	UpdateSessionState(ctx context.Context, id string, state string) error
	SetSessionNativeHandle(ctx context.Context, id, handle string) error

	// Sessions (additional)
	ListActiveSessions(ctx context.Context, orgID string) ([]Session, error)
	CountActiveSessionsByUser(ctx context.Context, userID string) (int, error)

	// Messages
	AppendMessage(ctx context.Context, msg *Message) (int64, error)
	GetMessages(ctx context.Context, sessionID string, afterSeq int64, limit int) ([]Message, error)
	MessageExists(ctx context.Context, sessionID, messageID string) (bool, error)

	// Endpoint Permissions
	GrantEndpointAccess(ctx context.Context, userID, endpointID string) error
	RevokeEndpointAccess(ctx context.Context, userID, endpointID string) error
	ListUserEndpoints(ctx context.Context, userID string) ([]string, error)
	HasEndpointAccess(ctx context.Context, userID, endpointID string) (bool, error)

	// Audit
	LogAuditEvent(ctx context.Context, event *AuditEvent) error
	ListAuditEvents(ctx context.Context, orgID string, limit, offset int) ([]AuditEvent, error)
	ListAuditEventsFiltered(ctx context.Context, orgID string, filter AuditFilter) ([]AuditEvent, error)

	// Data retention
	PurgeOldMessages(ctx context.Context, before time.Time) (int64, error)
	PurgeOldAuditEvents(ctx context.Context, before time.Time) (int64, error)

	// Admin
	ListAllSessions(ctx context.Context, orgID string) ([]Session, error)

	// Endpoint Config Overrides
	UpsertEndpointConfigOverride(ctx context.Context, override *EndpointConfigOverride) error
	GetEndpointConfigOverride(ctx context.Context, endpointID string) (*EndpointConfigOverride, error)
	ListEndpointConfigOverrides(ctx context.Context, orgID string) ([]EndpointConfigOverride, error)
	DeleteEndpointConfigOverride(ctx context.Context, endpointID string) error

	// Health
	Ping(ctx context.Context) error

	// Lifecycle
	Close() error
}

// Organization represents a tenant organization.
type Organization struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

// User represents a hub user.
type User struct {
	ID           string    `json:"id"`
	OrgID        string    `json:"org_id"`
	ExternalID   string    `json:"external_id,omitempty"` // external auth user_id or empty
	Username     string    `json:"username"`
	PasswordHash string    `json:"-"`
	Role         string    `json:"role"` // "admin" or "user"
	CreatedAt    time.Time `json:"created_at"`
}

// Runtime represents a registered runtime.
type Runtime struct {
	ID       string    `json:"id"`
	OrgID    string    `json:"org_id"`
	Name     string    `json:"name"`
	Online   bool      `json:"online"`
	LastSeen time.Time `json:"last_seen"`
}

// Endpoint represents an agent endpoint.
type Endpoint struct {
	ID        string `json:"id"`
	OrgID     string `json:"org_id"`
	RuntimeID string `json:"runtime_id"`
	Profile   string `json:"profile"`
	Name      string `json:"name"`
	Tags      string `json:"tags"`     // JSON-encoded map
	Caps      string `json:"caps"`     // JSON-encoded ProfileCaps
	Security  string `json:"security"` // JSON-encoded SecurityProfile
}

// Session represents a conversation session.
type Session struct {
	ID           string    `json:"id"`
	OrgID        string    `json:"org_id"`
	UserID       string    `json:"user_id"`
	EndpointID   string    `json:"endpoint_id"`
	RuntimeID    string    `json:"runtime_id"`
	Profile      string    `json:"profile"`
	State        string    `json:"state"` // "active", "idle", "closed"
	NativeHandle string    `json:"native_handle,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	EndpointName string    `json:"endpoint_name,omitempty"`
}

// Message represents a stored message in a transcript.
type Message struct {
	ID        string    `json:"id"`
	SessionID string    `json:"session_id"`
	Seq       int64     `json:"seq"`
	Direction string    `json:"direction"` // "user" or "agent"
	Channel   string    `json:"channel"`   // "stdin", "stdout", "stderr", "system"
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

// EndpointConfigOverride stores admin-set config overrides for an endpoint.
type EndpointConfigOverride struct {
	EndpointID string    `json:"endpoint_id"`
	OrgID      string    `json:"org_id"`
	Security   string    `json:"security"`   // JSON-encoded SecurityProfile
	Limits     string    `json:"limits"`     // JSON-encoded EndpointLimits
	UpdatedBy  string    `json:"updated_by"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// AuditEvent is a log entry for audit purposes.
type AuditEvent struct {
	ID         string          `json:"id"`
	OrgID      string          `json:"org_id"`
	Action     string          `json:"action"`
	UserID     string          `json:"user_id,omitempty"`
	RuntimeID  string          `json:"runtime_id,omitempty"`
	SessionID  string          `json:"session_id,omitempty"`
	EndpointID string          `json:"endpoint_id,omitempty"`
	Detail     json.RawMessage `json:"detail,omitempty"`
	CreatedAt  time.Time       `json:"created_at"`
}

// AuditFilter specifies criteria for filtering audit events.
type AuditFilter struct {
	Action     string
	UserID     string
	SessionID  string
	EndpointID string
	Limit      int
	Offset     int
}
