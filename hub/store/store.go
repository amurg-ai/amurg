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

	// Agents
	UpsertAgent(ctx context.Context, agent *Agent) error
	GetAgent(ctx context.Context, id string) (*Agent, error)
	ListAgents(ctx context.Context, orgID string) ([]Agent, error)
	ListAgentsByRuntime(ctx context.Context, runtimeID string) ([]Agent, error)
	DeleteAgentsByRuntime(ctx context.Context, runtimeID string) error

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

	// Agent Permissions
	GrantAgentAccess(ctx context.Context, userID, agentID string) error
	RevokeAgentAccess(ctx context.Context, userID, agentID string) error
	ListUserAgents(ctx context.Context, userID string) ([]string, error)
	HasAgentAccess(ctx context.Context, userID, agentID string) (bool, error)

	// Audit
	LogAuditEvent(ctx context.Context, event *AuditEvent) error
	ListAuditEvents(ctx context.Context, orgID string, limit, offset int) ([]AuditEvent, error)
	ListAuditEventsFiltered(ctx context.Context, orgID string, filter AuditFilter) ([]AuditEvent, error)

	// Data retention
	PurgeOldMessages(ctx context.Context, before time.Time) (int64, error)
	PurgeOldAuditEvents(ctx context.Context, before time.Time) (int64, error)

	// Admin
	ListAllSessions(ctx context.Context, orgID string) ([]Session, error)

	// Agent Config Overrides
	UpsertAgentConfigOverride(ctx context.Context, override *AgentConfigOverride) error
	GetAgentConfigOverride(ctx context.Context, agentID string) (*AgentConfigOverride, error)
	ListAgentConfigOverrides(ctx context.Context, orgID string) ([]AgentConfigOverride, error)
	DeleteAgentConfigOverride(ctx context.Context, agentID string) error

	// Device Codes
	CreateDeviceCode(ctx context.Context, dc *DeviceCode) error
	GetDeviceCodeByUserCode(ctx context.Context, userCode string) (*DeviceCode, error)
	GetDeviceCodeByPollingToken(ctx context.Context, pollingToken string) (*DeviceCode, error)
	UpdateDeviceCodeStatus(ctx context.Context, id, status, runtimeID, token, approvedBy string) error
	PurgeExpiredDeviceCodes(ctx context.Context) (int64, error)

	// Runtime Tokens
	CreateRuntimeToken(ctx context.Context, rt *RuntimeToken) error
	GetRuntimeTokenByHash(ctx context.Context, tokenHash string) (*RuntimeToken, error)
	ListRuntimeTokens(ctx context.Context, orgID string) ([]RuntimeToken, error)
	RevokeRuntimeToken(ctx context.Context, id string) error
	UpdateRuntimeTokenLastUsed(ctx context.Context, id string) error

	// Subscriptions (billing)
	GetSubscription(ctx context.Context, orgID string) (*Subscription, error)
	UpsertSubscription(ctx context.Context, sub *Subscription) error
	GetSubscriptionByStripeCustomer(ctx context.Context, customerID string) (*Subscription, error)

	// Billing counts
	CountActiveSessionsByOrg(ctx context.Context, orgID string) (int, error)
	CountOnlineRuntimesByOrg(ctx context.Context, orgID string) (int, error)

	// Health
	Ping(ctx context.Context) error

	// Lifecycle
	Close() error
}

// Organization represents a tenant organization.
type Organization struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Plan      string    `json:"plan"`
	CreatedAt time.Time `json:"created_at"`
}

// Subscription represents a billing subscription for an organization.
type Subscription struct {
	ID                   string    `json:"id"`
	OrgID                string    `json:"org_id"`
	StripeCustomerID     string    `json:"stripe_customer_id"`
	StripeSubscriptionID string    `json:"stripe_subscription_id"`
	Plan                 string    `json:"plan"`
	Status               string    `json:"status"`
	CurrentPeriodEnd     time.Time `json:"current_period_end"`
	CreatedAt            time.Time `json:"created_at"`
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

// Agent represents an agent.
type Agent struct {
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
	AgentID      string    `json:"agent_id"`
	RuntimeID    string    `json:"runtime_id"`
	Profile      string    `json:"profile"`
	State        string    `json:"state"` // "active", "idle", "closed"
	NativeHandle string    `json:"native_handle,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	AgentName    string    `json:"agent_name,omitempty"`
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

// AgentConfigOverride stores admin-set config overrides for an agent.
type AgentConfigOverride struct {
	AgentID   string    `json:"agent_id"`
	OrgID     string    `json:"org_id"`
	Security  string    `json:"security"`  // JSON-encoded SecurityProfile
	Limits    string    `json:"limits"`    // JSON-encoded AgentLimits
	UpdatedBy string    `json:"updated_by"`
	UpdatedAt time.Time `json:"updated_at"`
}

// AuditEvent is a log entry for audit purposes.
type AuditEvent struct {
	ID         string          `json:"id"`
	OrgID      string          `json:"org_id"`
	Action     string          `json:"action"`
	UserID     string          `json:"user_id,omitempty"`
	RuntimeID  string          `json:"runtime_id,omitempty"`
	SessionID  string          `json:"session_id,omitempty"`
	AgentID    string          `json:"agent_id,omitempty"`
	Detail     json.RawMessage `json:"detail,omitempty"`
	CreatedAt  time.Time       `json:"created_at"`
}

// DeviceCode represents a device authorization code for runtime registration.
type DeviceCode struct {
	ID           string    `json:"id"`
	UserCode     string    `json:"user_code"`
	PollingToken string    `json:"polling_token"`
	OrgID        string    `json:"org_id"`
	Status       string    `json:"status"` // pending, approved, expired
	RuntimeID    string    `json:"runtime_id"`
	Token        string    `json:"token"`
	ApprovedBy   string    `json:"approved_by"`
	CreatedAt    time.Time `json:"created_at"`
	ExpiresAt    time.Time `json:"expires_at"`
}

// RuntimeToken represents a long-lived authentication token for a runtime.
type RuntimeToken struct {
	ID         string     `json:"id"`
	OrgID      string     `json:"org_id"`
	RuntimeID  string     `json:"runtime_id"`
	TokenHash  string     `json:"token_hash"`
	Name       string     `json:"name"`
	CreatedBy  string     `json:"created_by"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
}

// AuditFilter specifies criteria for filtering audit events.
type AuditFilter struct {
	Action     string
	UserID     string
	SessionID  string
	AgentID string
	Limit   int
	Offset  int
}
