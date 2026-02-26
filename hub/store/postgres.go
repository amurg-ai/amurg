package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// PostgresStore implements Store using PostgreSQL.
type PostgresStore struct {
	db *sql.DB
}

// NewPostgres creates a new PostgreSQL store and runs migrations.
func NewPostgres(dsn string) (*PostgresStore, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}

	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(10)
	db.SetConnMaxLifetime(5 * time.Minute)

	s := &PostgresStore{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}

	return s, nil
}

func pgTableExists(db *sql.DB, name string) bool {
	var exists bool
	_ = db.QueryRow("SELECT EXISTS(SELECT 1 FROM information_schema.tables WHERE table_name=$1)", name).Scan(&exists)
	return exists
}

func (s *PostgresStore) migrate() error {
	migrations := []string{
		`CREATE TABLE IF NOT EXISTS organizations (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`INSERT INTO organizations (id, name) VALUES ('default', 'Default')
		 ON CONFLICT(id) DO NOTHING`,
		`CREATE TABLE IF NOT EXISTS users (
			id TEXT PRIMARY KEY,
			org_id TEXT NOT NULL DEFAULT 'default' REFERENCES organizations(id),
			external_id TEXT NOT NULL DEFAULT '',
			username TEXT NOT NULL,
			password_hash TEXT NOT NULL,
			role TEXT NOT NULL DEFAULT 'user',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			UNIQUE(org_id, username)
		)`,
		`CREATE TABLE IF NOT EXISTS runtimes (
			id TEXT PRIMARY KEY,
			org_id TEXT NOT NULL DEFAULT 'default' REFERENCES organizations(id),
			name TEXT NOT NULL DEFAULT '',
			online BOOLEAN NOT NULL DEFAULT FALSE,
			last_seen TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE TABLE IF NOT EXISTS agents (
			id TEXT PRIMARY KEY,
			org_id TEXT NOT NULL DEFAULT 'default' REFERENCES organizations(id),
			runtime_id TEXT NOT NULL REFERENCES runtimes(id),
			profile TEXT NOT NULL,
			name TEXT NOT NULL DEFAULT '',
			tags JSONB NOT NULL DEFAULT '{}',
			caps JSONB NOT NULL DEFAULT '{}',
			security JSONB NOT NULL DEFAULT '{}'
		)`,
		`CREATE TABLE IF NOT EXISTS sessions (
			id TEXT PRIMARY KEY,
			org_id TEXT NOT NULL DEFAULT 'default' REFERENCES organizations(id),
			user_id TEXT NOT NULL,
			agent_id TEXT NOT NULL,
			runtime_id TEXT NOT NULL,
			profile TEXT NOT NULL,
			state TEXT NOT NULL DEFAULT 'active',
			native_handle TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE TABLE IF NOT EXISTS messages (
			id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL REFERENCES sessions(id),
			seq BIGINT NOT NULL,
			direction TEXT NOT NULL,
			channel TEXT NOT NULL DEFAULT 'stdout',
			content TEXT NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_messages_session_seq ON messages(session_id, seq)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_user_id ON sessions(user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_state ON sessions(state)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_org_id ON sessions(org_id)`,
		`CREATE INDEX IF NOT EXISTS idx_runtimes_org_id ON runtimes(org_id)`,
		`CREATE INDEX IF NOT EXISTS idx_agents_org_id ON agents(org_id)`,
		`CREATE TABLE IF NOT EXISTS agent_permissions (
			user_id TEXT NOT NULL,
			agent_id TEXT NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			PRIMARY KEY (user_id, agent_id)
		)`,
		`CREATE TABLE IF NOT EXISTS audit_events (
			id TEXT PRIMARY KEY,
			org_id TEXT NOT NULL DEFAULT 'default',
			action TEXT NOT NULL,
			user_id TEXT NOT NULL DEFAULT '',
			runtime_id TEXT NOT NULL DEFAULT '',
			session_id TEXT NOT NULL DEFAULT '',
			agent_id TEXT NOT NULL DEFAULT '',
			detail TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_audit_events_created_at ON audit_events(created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_audit_events_org_id ON audit_events(org_id)`,
		`CREATE INDEX IF NOT EXISTS idx_audit_events_action ON audit_events(action)`,
		`CREATE INDEX IF NOT EXISTS idx_audit_events_agent_id ON audit_events(agent_id)`,
		// Agent config overrides
		`CREATE TABLE IF NOT EXISTS agent_config_overrides (
			agent_id TEXT PRIMARY KEY,
			org_id TEXT NOT NULL DEFAULT 'default',
			security JSONB NOT NULL DEFAULT '{}',
			limits JSONB NOT NULL DEFAULT '{}',
			updated_by TEXT NOT NULL DEFAULT '',
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
	}

	for _, m := range migrations {
		if _, err := s.db.Exec(m); err != nil {
			return fmt.Errorf("migration failed: %w\n  SQL: %s", err, m)
		}
	}

	// Device codes and runtime tokens tables.
	deviceCodeMigrations := []string{
		`CREATE TABLE IF NOT EXISTS device_codes (
			id TEXT PRIMARY KEY,
			user_code TEXT UNIQUE NOT NULL,
			polling_token TEXT UNIQUE NOT NULL,
			org_id TEXT NOT NULL DEFAULT 'default',
			status TEXT NOT NULL DEFAULT 'pending',
			runtime_id TEXT NOT NULL DEFAULT '',
			token TEXT NOT NULL DEFAULT '',
			approved_by TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			expires_at TIMESTAMPTZ NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS runtime_tokens (
			id TEXT PRIMARY KEY,
			org_id TEXT NOT NULL DEFAULT 'default',
			runtime_id TEXT NOT NULL,
			token_hash TEXT NOT NULL,
			name TEXT NOT NULL DEFAULT '',
			created_by TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			last_used_at TIMESTAMPTZ
		)`,
		`CREATE INDEX IF NOT EXISTS idx_runtime_tokens_hash ON runtime_tokens(token_hash)`,
		`CREATE INDEX IF NOT EXISTS idx_runtime_tokens_org ON runtime_tokens(org_id)`,
	}
	for _, m := range deviceCodeMigrations {
		if _, err := s.db.Exec(m); err != nil {
			return fmt.Errorf("migration failed: %w\n  SQL: %s", err, m)
		}
	}

	// Subscriptions (billing)
	subscriptionMigrations := []string{
		`CREATE TABLE IF NOT EXISTS subscriptions (
			id TEXT PRIMARY KEY,
			org_id TEXT NOT NULL UNIQUE,
			stripe_customer_id TEXT NOT NULL DEFAULT '',
			stripe_subscription_id TEXT NOT NULL DEFAULT '',
			plan TEXT NOT NULL DEFAULT 'free',
			status TEXT NOT NULL DEFAULT 'active',
			current_period_end TIMESTAMPTZ,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_subscriptions_org_id ON subscriptions(org_id)`,
		`CREATE INDEX IF NOT EXISTS idx_subscriptions_stripe_customer ON subscriptions(stripe_customer_id)`,
		// Add plan column to organizations (idempotent)
		`DO $$ BEGIN
			ALTER TABLE organizations ADD COLUMN plan TEXT NOT NULL DEFAULT 'free';
		EXCEPTION WHEN duplicate_column THEN NULL;
		END $$`,
	}
	for _, m := range subscriptionMigrations {
		if _, err := s.db.Exec(m); err != nil {
			return fmt.Errorf("migration failed: %w\n  SQL: %s", err, m)
		}
	}

	// Phase: rename endpoint -> agent (migration for existing databases)
	if pgTableExists(s.db, "endpoints") {
		renameStmts := []string{
			`ALTER TABLE endpoints RENAME TO agents`,
			`ALTER TABLE endpoint_permissions RENAME TO agent_permissions`,
			`ALTER TABLE endpoint_config_overrides RENAME TO agent_config_overrides`,
			`ALTER TABLE sessions RENAME COLUMN endpoint_id TO agent_id`,
			`ALTER TABLE audit_events RENAME COLUMN endpoint_id TO agent_id`,
			`ALTER TABLE agent_permissions RENAME COLUMN endpoint_id TO agent_id`,
			`ALTER TABLE agent_config_overrides RENAME COLUMN endpoint_id TO agent_id`,
			`DROP INDEX IF EXISTS idx_endpoints_org_id`,
			`CREATE INDEX IF NOT EXISTS idx_agents_org_id ON agents(org_id)`,
			`DROP INDEX IF EXISTS idx_audit_events_endpoint_id`,
			`CREATE INDEX IF NOT EXISTS idx_audit_events_agent_id ON audit_events(agent_id)`,
		}
		for _, stmt := range renameStmts {
			if _, err := s.db.Exec(stmt); err != nil {
				return fmt.Errorf("rename migration failed: %w\n  SQL: %s", err, stmt)
			}
		}
	}

	// Fix partial rename state: agent_config_overrides may still have endpoint_id
	// column if a previous migration was partially applied.
	if pgTableExists(s.db, "agent_config_overrides") {
		var hasEndpointID bool
		_ = s.db.QueryRow(
			`SELECT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_name='agent_config_overrides' AND column_name='endpoint_id')`,
		).Scan(&hasEndpointID)
		if hasEndpointID {
			if _, err := s.db.Exec(`ALTER TABLE agent_config_overrides RENAME COLUMN endpoint_id TO agent_id`); err != nil {
				return fmt.Errorf("fix agent_config_overrides column: %w", err)
			}
		}
	}

	// Drop sessions_user_id_fkey if it exists: sessions.user_id stores external
	// IDs (e.g. Clerk user IDs), not the internal UUID from users(id).
	_, _ = s.db.Exec(`ALTER TABLE sessions DROP CONSTRAINT IF EXISTS sessions_user_id_fkey`)

	return nil
}

func (s *PostgresStore) Ping(ctx context.Context) error {
	return s.db.PingContext(ctx)
}

func (s *PostgresStore) Close() error {
	return s.db.Close()
}

// --- Organizations ---

func (s *PostgresStore) CreateOrganization(ctx context.Context, org *Organization) error {
	_, err := s.db.ExecContext(ctx,
		"INSERT INTO organizations (id, name, created_at) VALUES ($1, $2, $3) ON CONFLICT(id) DO NOTHING",
		org.ID, org.Name, org.CreatedAt,
	)
	return err
}

func (s *PostgresStore) GetOrganization(ctx context.Context, id string) (*Organization, error) {
	var org Organization
	err := s.db.QueryRowContext(ctx,
		"SELECT id, name, created_at FROM organizations WHERE id = $1", id,
	).Scan(&org.ID, &org.Name, &org.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &org, err
}

// --- Users ---

func (s *PostgresStore) CreateUser(ctx context.Context, user *User) error {
	_, err := s.db.ExecContext(ctx,
		"INSERT INTO users (id, org_id, external_id, username, password_hash, role, created_at) VALUES ($1, $2, $3, $4, $5, $6, $7)",
		user.ID, user.OrgID, user.ExternalID, user.Username, user.PasswordHash, user.Role, user.CreatedAt,
	)
	return err
}

func (s *PostgresStore) GetUser(ctx context.Context, orgID, username string) (*User, error) {
	var u User
	err := s.db.QueryRowContext(ctx,
		"SELECT id, org_id, external_id, username, password_hash, role, created_at FROM users WHERE org_id = $1 AND username = $2",
		orgID, username,
	).Scan(&u.ID, &u.OrgID, &u.ExternalID, &u.Username, &u.PasswordHash, &u.Role, &u.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &u, err
}

func (s *PostgresStore) GetUserByID(ctx context.Context, id string) (*User, error) {
	var u User
	err := s.db.QueryRowContext(ctx,
		"SELECT id, org_id, external_id, username, password_hash, role, created_at FROM users WHERE id = $1", id,
	).Scan(&u.ID, &u.OrgID, &u.ExternalID, &u.Username, &u.PasswordHash, &u.Role, &u.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &u, err
}

func (s *PostgresStore) GetUserByExternalID(ctx context.Context, externalID string) (*User, error) {
	var u User
	err := s.db.QueryRowContext(ctx,
		"SELECT id, org_id, external_id, username, password_hash, role, created_at FROM users WHERE external_id = $1",
		externalID,
	).Scan(&u.ID, &u.OrgID, &u.ExternalID, &u.Username, &u.PasswordHash, &u.Role, &u.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &u, err
}

func (s *PostgresStore) ListUsers(ctx context.Context, orgID string) ([]User, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT id, org_id, external_id, username, password_hash, role, created_at FROM users WHERE org_id = $1 ORDER BY created_at",
		orgID,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var users []User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.OrgID, &u.ExternalID, &u.Username, &u.PasswordHash, &u.Role, &u.CreatedAt); err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

// --- Runtimes ---

func (s *PostgresStore) UpsertRuntime(ctx context.Context, rt *Runtime) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO runtimes (id, org_id, name, online, last_seen) VALUES ($1, $2, $3, $4, $5)
		 ON CONFLICT(id) DO UPDATE SET org_id=EXCLUDED.org_id, name=EXCLUDED.name, online=EXCLUDED.online, last_seen=EXCLUDED.last_seen`,
		rt.ID, rt.OrgID, rt.Name, rt.Online, rt.LastSeen,
	)
	return err
}

func (s *PostgresStore) GetRuntime(ctx context.Context, id string) (*Runtime, error) {
	var rt Runtime
	err := s.db.QueryRowContext(ctx,
		"SELECT id, org_id, name, online, last_seen FROM runtimes WHERE id = $1", id,
	).Scan(&rt.ID, &rt.OrgID, &rt.Name, &rt.Online, &rt.LastSeen)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &rt, err
}

func (s *PostgresStore) ListRuntimes(ctx context.Context, orgID string) ([]Runtime, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT id, org_id, name, online, last_seen FROM runtimes WHERE org_id = $1 ORDER BY name",
		orgID,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var runtimes []Runtime
	for rows.Next() {
		var rt Runtime
		if err := rows.Scan(&rt.ID, &rt.OrgID, &rt.Name, &rt.Online, &rt.LastSeen); err != nil {
			return nil, err
		}
		runtimes = append(runtimes, rt)
	}
	return runtimes, rows.Err()
}

func (s *PostgresStore) SetRuntimeOnline(ctx context.Context, id string, online bool) error {
	_, err := s.db.ExecContext(ctx,
		"UPDATE runtimes SET online = $1, last_seen = $2 WHERE id = $3",
		online, time.Now(), id,
	)
	return err
}

// --- Agents ---

func (s *PostgresStore) UpsertAgent(ctx context.Context, agent *Agent) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO agents (id, org_id, runtime_id, profile, name, tags, caps, security) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		 ON CONFLICT(id) DO UPDATE SET org_id=EXCLUDED.org_id, runtime_id=EXCLUDED.runtime_id, profile=EXCLUDED.profile, name=EXCLUDED.name, tags=EXCLUDED.tags, caps=EXCLUDED.caps, security=EXCLUDED.security`,
		agent.ID, agent.OrgID, agent.RuntimeID, agent.Profile, agent.Name, agent.Tags, agent.Caps, agent.Security,
	)
	return err
}

func (s *PostgresStore) GetAgent(ctx context.Context, id string) (*Agent, error) {
	var agent Agent
	err := s.db.QueryRowContext(ctx,
		"SELECT id, org_id, runtime_id, profile, name, tags, caps, security FROM agents WHERE id = $1", id,
	).Scan(&agent.ID, &agent.OrgID, &agent.RuntimeID, &agent.Profile, &agent.Name, &agent.Tags, &agent.Caps, &agent.Security)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &agent, err
}

func (s *PostgresStore) ListAgents(ctx context.Context, orgID string) ([]Agent, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT id, org_id, runtime_id, profile, name, tags, caps, security FROM agents WHERE org_id = $1 ORDER BY name",
		orgID,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var agents []Agent
	for rows.Next() {
		var agent Agent
		if err := rows.Scan(&agent.ID, &agent.OrgID, &agent.RuntimeID, &agent.Profile, &agent.Name, &agent.Tags, &agent.Caps, &agent.Security); err != nil {
			return nil, err
		}
		agents = append(agents, agent)
	}
	return agents, rows.Err()
}

func (s *PostgresStore) ListAgentsByRuntime(ctx context.Context, runtimeID string) ([]Agent, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT id, org_id, runtime_id, profile, name, tags, caps, security FROM agents WHERE runtime_id = $1 ORDER BY name",
		runtimeID,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var agents []Agent
	for rows.Next() {
		var agent Agent
		if err := rows.Scan(&agent.ID, &agent.OrgID, &agent.RuntimeID, &agent.Profile, &agent.Name, &agent.Tags, &agent.Caps, &agent.Security); err != nil {
			return nil, err
		}
		agents = append(agents, agent)
	}
	return agents, rows.Err()
}

func (s *PostgresStore) DeleteAgentsByRuntime(ctx context.Context, runtimeID string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM agents WHERE runtime_id = $1", runtimeID)
	return err
}

// --- Sessions ---

func (s *PostgresStore) CreateSession(ctx context.Context, sess *Session) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO sessions (id, org_id, user_id, agent_id, runtime_id, profile, state, native_handle, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		sess.ID, sess.OrgID, sess.UserID, sess.AgentID, sess.RuntimeID, sess.Profile,
		sess.State, sess.NativeHandle, sess.CreatedAt, sess.UpdatedAt,
	)
	return err
}

func (s *PostgresStore) GetSession(ctx context.Context, id string) (*Session, error) {
	var sess Session
	err := s.db.QueryRowContext(ctx,
		`SELECT id, org_id, user_id, agent_id, runtime_id, profile, state, native_handle, created_at, updated_at
		 FROM sessions WHERE id = $1`, id,
	).Scan(&sess.ID, &sess.OrgID, &sess.UserID, &sess.AgentID, &sess.RuntimeID, &sess.Profile,
		&sess.State, &sess.NativeHandle, &sess.CreatedAt, &sess.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &sess, err
}

func (s *PostgresStore) ListSessionsByUser(ctx context.Context, userID string) ([]Session, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT s.id, s.org_id, s.user_id, s.agent_id, s.runtime_id, s.profile, s.state, s.native_handle,
		        s.created_at, s.updated_at, COALESCE(a.name, '') as agent_name
		 FROM sessions s LEFT JOIN agents a ON s.agent_id = a.id
		 WHERE s.user_id = $1 ORDER BY s.updated_at DESC`, userID,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var sessions []Session
	for rows.Next() {
		var sess Session
		if err := rows.Scan(&sess.ID, &sess.OrgID, &sess.UserID, &sess.AgentID, &sess.RuntimeID, &sess.Profile,
			&sess.State, &sess.NativeHandle, &sess.CreatedAt, &sess.UpdatedAt, &sess.AgentName); err != nil {
			return nil, err
		}
		sessions = append(sessions, sess)
	}
	return sessions, rows.Err()
}

func (s *PostgresStore) UpdateSessionState(ctx context.Context, id string, state string) error {
	_, err := s.db.ExecContext(ctx,
		"UPDATE sessions SET state = $1, updated_at = $2 WHERE id = $3",
		state, time.Now(), id,
	)
	return err
}

func (s *PostgresStore) SetSessionNativeHandle(ctx context.Context, id, handle string) error {
	_, err := s.db.ExecContext(ctx,
		"UPDATE sessions SET native_handle = $1, updated_at = $2 WHERE id = $3",
		handle, time.Now(), id,
	)
	return err
}

// --- Messages ---

func (s *PostgresStore) AppendMessage(ctx context.Context, msg *Message) (int64, error) {
	var seq int64
	err := s.db.QueryRowContext(ctx,
		`INSERT INTO messages (id, session_id, seq, direction, channel, content, created_at)
		 VALUES ($1, $2, (SELECT COALESCE(MAX(seq),0)+1 FROM messages WHERE session_id = $3), $4, $5, $6, $7)
		 RETURNING seq`,
		msg.ID, msg.SessionID, msg.SessionID, msg.Direction, msg.Channel, msg.Content, msg.CreatedAt,
	).Scan(&seq)
	return seq, err
}

func (s *PostgresStore) GetMessages(ctx context.Context, sessionID string, afterSeq int64, limit int) ([]Message, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, session_id, seq, direction, channel, content, created_at
		 FROM messages WHERE session_id = $1 AND seq > $2 ORDER BY seq LIMIT $3`,
		sessionID, afterSeq, limit,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var messages []Message
	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.ID, &m.SessionID, &m.Seq, &m.Direction, &m.Channel, &m.Content, &m.CreatedAt); err != nil {
			return nil, err
		}
		messages = append(messages, m)
	}
	return messages, rows.Err()
}

func (s *PostgresStore) MessageExists(ctx context.Context, sessionID, messageID string) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM messages WHERE session_id = $1 AND id = $2", sessionID, messageID,
	).Scan(&count)
	return count > 0, err
}

// --- Sessions (additional) ---

func (s *PostgresStore) ListActiveSessions(ctx context.Context, orgID string) ([]Session, error) {
	var rows *sql.Rows
	var err error
	if orgID == "" {
		rows, err = s.db.QueryContext(ctx,
			`SELECT id, org_id, user_id, agent_id, runtime_id, profile, state, native_handle, created_at, updated_at
			 FROM sessions WHERE state NOT IN ('closed') ORDER BY updated_at DESC`)
	} else {
		rows, err = s.db.QueryContext(ctx,
			`SELECT id, org_id, user_id, agent_id, runtime_id, profile, state, native_handle, created_at, updated_at
			 FROM sessions WHERE org_id = $1 AND state NOT IN ('closed') ORDER BY updated_at DESC`,
			orgID)
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var sessions []Session
	for rows.Next() {
		var sess Session
		if err := rows.Scan(&sess.ID, &sess.OrgID, &sess.UserID, &sess.AgentID, &sess.RuntimeID, &sess.Profile,
			&sess.State, &sess.NativeHandle, &sess.CreatedAt, &sess.UpdatedAt); err != nil {
			return nil, err
		}
		sessions = append(sessions, sess)
	}
	return sessions, rows.Err()
}

func (s *PostgresStore) CountActiveSessionsByUser(ctx context.Context, userID string) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM sessions WHERE user_id = $1 AND state NOT IN ('closed')", userID,
	).Scan(&count)
	return count, err
}

// --- Agent Permissions ---

func (s *PostgresStore) GrantAgentAccess(ctx context.Context, userID, agentID string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO agent_permissions (user_id, agent_id, created_at) VALUES ($1, $2, $3)
		 ON CONFLICT(user_id, agent_id) DO NOTHING`,
		userID, agentID, time.Now(),
	)
	return err
}

func (s *PostgresStore) RevokeAgentAccess(ctx context.Context, userID, agentID string) error {
	_, err := s.db.ExecContext(ctx,
		"DELETE FROM agent_permissions WHERE user_id = $1 AND agent_id = $2",
		userID, agentID,
	)
	return err
}

func (s *PostgresStore) ListUserAgents(ctx context.Context, userID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT agent_id FROM agent_permissions WHERE user_id = $1", userID,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *PostgresStore) HasAgentAccess(ctx context.Context, userID, agentID string) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM agent_permissions WHERE user_id = $1 AND agent_id = $2",
		userID, agentID,
	).Scan(&count)
	return count > 0, err
}

// --- Audit ---

func (s *PostgresStore) LogAuditEvent(ctx context.Context, event *AuditEvent) error {
	detail := ""
	if event.Detail != nil {
		detail = string(event.Detail)
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO audit_events (id, org_id, action, user_id, runtime_id, session_id, agent_id, detail, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		event.ID, event.OrgID, event.Action, event.UserID, event.RuntimeID, event.SessionID, event.AgentID, detail, event.CreatedAt,
	)
	return err
}

func (s *PostgresStore) ListAuditEvents(ctx context.Context, orgID string, limit, offset int) ([]AuditEvent, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, org_id, action, user_id, runtime_id, session_id, agent_id, detail, created_at
		 FROM audit_events WHERE org_id = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3`,
		orgID, limit, offset,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var events []AuditEvent
	for rows.Next() {
		var e AuditEvent
		var detail string
		if err := rows.Scan(&e.ID, &e.OrgID, &e.Action, &e.UserID, &e.RuntimeID, &e.SessionID, &e.AgentID, &detail, &e.CreatedAt); err != nil {
			return nil, err
		}
		if detail != "" {
			e.Detail = json.RawMessage(detail)
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

func (s *PostgresStore) ListAuditEventsFiltered(ctx context.Context, orgID string, filter AuditFilter) ([]AuditEvent, error) {
	query := `SELECT id, org_id, action, user_id, runtime_id, session_id, agent_id, detail, created_at
	          FROM audit_events WHERE org_id = $1`
	args := []any{orgID}
	argN := 2

	if filter.Action != "" {
		query += fmt.Sprintf(" AND action LIKE $%d", argN)
		args = append(args, filter.Action+"%")
		argN++
	}
	if filter.UserID != "" {
		query += fmt.Sprintf(" AND user_id = $%d", argN)
		args = append(args, filter.UserID)
		argN++
	}
	if filter.SessionID != "" {
		query += fmt.Sprintf(" AND session_id = $%d", argN)
		args = append(args, filter.SessionID)
		argN++
	}
	if filter.AgentID != "" {
		query += fmt.Sprintf(" AND agent_id = $%d", argN)
		args = append(args, filter.AgentID)
		argN++
	}

	query += " ORDER BY created_at DESC"

	limit := filter.Limit
	if limit <= 0 {
		limit = 50
	}
	query += fmt.Sprintf(" LIMIT $%d", argN)
	args = append(args, limit)
	argN++

	if filter.Offset > 0 {
		query += fmt.Sprintf(" OFFSET $%d", argN)
		args = append(args, filter.Offset)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var events []AuditEvent
	for rows.Next() {
		var e AuditEvent
		var detail string
		if err := rows.Scan(&e.ID, &e.OrgID, &e.Action, &e.UserID, &e.RuntimeID, &e.SessionID, &e.AgentID, &detail, &e.CreatedAt); err != nil {
			return nil, err
		}
		if detail != "" {
			e.Detail = json.RawMessage(detail)
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

// --- Admin ---

func (s *PostgresStore) ListAllSessions(ctx context.Context, orgID string) ([]Session, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT s.id, s.org_id, s.user_id, s.agent_id, s.runtime_id, s.profile, s.state, s.native_handle,
		        s.created_at, s.updated_at, COALESCE(a.name, '') as agent_name
		 FROM sessions s LEFT JOIN agents a ON s.agent_id = a.id
		 WHERE s.org_id = $1
		 ORDER BY s.updated_at DESC`,
		orgID,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var sessions []Session
	for rows.Next() {
		var sess Session
		if err := rows.Scan(&sess.ID, &sess.OrgID, &sess.UserID, &sess.AgentID, &sess.RuntimeID, &sess.Profile,
			&sess.State, &sess.NativeHandle, &sess.CreatedAt, &sess.UpdatedAt, &sess.AgentName); err != nil {
			return nil, err
		}
		sessions = append(sessions, sess)
	}
	return sessions, rows.Err()
}

// --- Data Retention ---

func (s *PostgresStore) PurgeOldMessages(ctx context.Context, before time.Time) (int64, error) {
	result, err := s.db.ExecContext(ctx,
		"DELETE FROM messages WHERE created_at < $1", before,
	)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (s *PostgresStore) PurgeOldAuditEvents(ctx context.Context, before time.Time) (int64, error) {
	result, err := s.db.ExecContext(ctx,
		"DELETE FROM audit_events WHERE created_at < $1", before,
	)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// --- Agent Config Overrides ---

func (s *PostgresStore) UpsertAgentConfigOverride(ctx context.Context, override *AgentConfigOverride) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO agent_config_overrides (agent_id, org_id, security, limits, updated_by, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 ON CONFLICT(agent_id) DO UPDATE SET
		   security = EXCLUDED.security,
		   limits = EXCLUDED.limits,
		   updated_by = EXCLUDED.updated_by,
		   updated_at = EXCLUDED.updated_at`,
		override.AgentID, override.OrgID, override.Security, override.Limits,
		override.UpdatedBy, override.UpdatedAt,
	)
	return err
}

func (s *PostgresStore) GetAgentConfigOverride(ctx context.Context, agentID string) (*AgentConfigOverride, error) {
	var o AgentConfigOverride
	err := s.db.QueryRowContext(ctx,
		"SELECT agent_id, org_id, security, limits, updated_by, updated_at FROM agent_config_overrides WHERE agent_id = $1",
		agentID,
	).Scan(&o.AgentID, &o.OrgID, &o.Security, &o.Limits, &o.UpdatedBy, &o.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &o, err
}

func (s *PostgresStore) ListAgentConfigOverrides(ctx context.Context, orgID string) ([]AgentConfigOverride, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT agent_id, org_id, security, limits, updated_by, updated_at FROM agent_config_overrides WHERE org_id = $1",
		orgID,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var overrides []AgentConfigOverride
	for rows.Next() {
		var o AgentConfigOverride
		if err := rows.Scan(&o.AgentID, &o.OrgID, &o.Security, &o.Limits, &o.UpdatedBy, &o.UpdatedAt); err != nil {
			return nil, err
		}
		overrides = append(overrides, o)
	}
	return overrides, rows.Err()
}

func (s *PostgresStore) DeleteAgentConfigOverride(ctx context.Context, agentID string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM agent_config_overrides WHERE agent_id = $1", agentID)
	return err
}

// --- Device Codes ---

func (s *PostgresStore) CreateDeviceCode(ctx context.Context, dc *DeviceCode) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO device_codes (id, user_code, polling_token, org_id, status, runtime_id, token, approved_by, created_at, expires_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		dc.ID, dc.UserCode, dc.PollingToken, dc.OrgID, dc.Status, dc.RuntimeID, dc.Token, dc.ApprovedBy, dc.CreatedAt, dc.ExpiresAt,
	)
	return err
}

func (s *PostgresStore) GetDeviceCodeByUserCode(ctx context.Context, userCode string) (*DeviceCode, error) {
	var dc DeviceCode
	err := s.db.QueryRowContext(ctx,
		`SELECT id, user_code, polling_token, org_id, status, runtime_id, token, approved_by, created_at, expires_at
		 FROM device_codes WHERE user_code = $1`, userCode,
	).Scan(&dc.ID, &dc.UserCode, &dc.PollingToken, &dc.OrgID, &dc.Status, &dc.RuntimeID, &dc.Token, &dc.ApprovedBy, &dc.CreatedAt, &dc.ExpiresAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &dc, err
}

func (s *PostgresStore) GetDeviceCodeByPollingToken(ctx context.Context, pollingToken string) (*DeviceCode, error) {
	var dc DeviceCode
	err := s.db.QueryRowContext(ctx,
		`SELECT id, user_code, polling_token, org_id, status, runtime_id, token, approved_by, created_at, expires_at
		 FROM device_codes WHERE polling_token = $1`, pollingToken,
	).Scan(&dc.ID, &dc.UserCode, &dc.PollingToken, &dc.OrgID, &dc.Status, &dc.RuntimeID, &dc.Token, &dc.ApprovedBy, &dc.CreatedAt, &dc.ExpiresAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &dc, err
}

func (s *PostgresStore) UpdateDeviceCodeStatus(ctx context.Context, id, status, runtimeID, token, approvedBy string) error {
	_, err := s.db.ExecContext(ctx,
		"UPDATE device_codes SET status = $1, runtime_id = $2, token = $3, approved_by = $4 WHERE id = $5",
		status, runtimeID, token, approvedBy, id,
	)
	return err
}

func (s *PostgresStore) PurgeExpiredDeviceCodes(ctx context.Context) (int64, error) {
	result, err := s.db.ExecContext(ctx,
		"DELETE FROM device_codes WHERE expires_at < $1 OR status IN ('approved', 'expired')",
		time.Now(),
	)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// --- Runtime Tokens ---

func (s *PostgresStore) CreateRuntimeToken(ctx context.Context, rt *RuntimeToken) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO runtime_tokens (id, org_id, runtime_id, token_hash, name, created_by, created_at, last_used_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		rt.ID, rt.OrgID, rt.RuntimeID, rt.TokenHash, rt.Name, rt.CreatedBy, rt.CreatedAt, rt.LastUsedAt,
	)
	return err
}

func (s *PostgresStore) GetRuntimeTokenByHash(ctx context.Context, tokenHash string) (*RuntimeToken, error) {
	var rt RuntimeToken
	err := s.db.QueryRowContext(ctx,
		`SELECT id, org_id, runtime_id, token_hash, name, created_by, created_at, last_used_at
		 FROM runtime_tokens WHERE token_hash = $1`, tokenHash,
	).Scan(&rt.ID, &rt.OrgID, &rt.RuntimeID, &rt.TokenHash, &rt.Name, &rt.CreatedBy, &rt.CreatedAt, &rt.LastUsedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &rt, err
}

func (s *PostgresStore) ListRuntimeTokens(ctx context.Context, orgID string) ([]RuntimeToken, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, org_id, runtime_id, token_hash, name, created_by, created_at, last_used_at
		 FROM runtime_tokens WHERE org_id = $1 ORDER BY created_at DESC`, orgID,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var tokens []RuntimeToken
	for rows.Next() {
		var rt RuntimeToken
		if err := rows.Scan(&rt.ID, &rt.OrgID, &rt.RuntimeID, &rt.TokenHash, &rt.Name, &rt.CreatedBy, &rt.CreatedAt, &rt.LastUsedAt); err != nil {
			return nil, err
		}
		tokens = append(tokens, rt)
	}
	return tokens, rows.Err()
}

func (s *PostgresStore) RevokeRuntimeToken(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM runtime_tokens WHERE id = $1", id)
	return err
}

func (s *PostgresStore) UpdateRuntimeTokenLastUsed(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx,
		"UPDATE runtime_tokens SET last_used_at = $1 WHERE id = $2",
		time.Now(), id,
	)
	return err
}

// --- Subscriptions (billing) ---

func (s *PostgresStore) GetSubscription(ctx context.Context, orgID string) (*Subscription, error) {
	var sub Subscription
	err := s.db.QueryRowContext(ctx,
		`SELECT id, org_id, stripe_customer_id, stripe_subscription_id, plan, status, current_period_end, created_at
		 FROM subscriptions WHERE org_id = $1`, orgID,
	).Scan(&sub.ID, &sub.OrgID, &sub.StripeCustomerID, &sub.StripeSubscriptionID,
		&sub.Plan, &sub.Status, &sub.CurrentPeriodEnd, &sub.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &sub, err
}

func (s *PostgresStore) UpsertSubscription(ctx context.Context, sub *Subscription) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO subscriptions (id, org_id, stripe_customer_id, stripe_subscription_id, plan, status, current_period_end, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		 ON CONFLICT(org_id) DO UPDATE SET
		   stripe_customer_id = EXCLUDED.stripe_customer_id,
		   stripe_subscription_id = EXCLUDED.stripe_subscription_id,
		   plan = EXCLUDED.plan,
		   status = EXCLUDED.status,
		   current_period_end = EXCLUDED.current_period_end`,
		sub.ID, sub.OrgID, sub.StripeCustomerID, sub.StripeSubscriptionID,
		sub.Plan, sub.Status, sub.CurrentPeriodEnd, sub.CreatedAt,
	)
	return err
}

func (s *PostgresStore) GetSubscriptionByStripeCustomer(ctx context.Context, customerID string) (*Subscription, error) {
	var sub Subscription
	err := s.db.QueryRowContext(ctx,
		`SELECT id, org_id, stripe_customer_id, stripe_subscription_id, plan, status, current_period_end, created_at
		 FROM subscriptions WHERE stripe_customer_id = $1`, customerID,
	).Scan(&sub.ID, &sub.OrgID, &sub.StripeCustomerID, &sub.StripeSubscriptionID,
		&sub.Plan, &sub.Status, &sub.CurrentPeriodEnd, &sub.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &sub, err
}

func (s *PostgresStore) CountActiveSessionsByOrg(ctx context.Context, orgID string) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM sessions WHERE org_id = $1 AND state NOT IN ('closed')", orgID,
	).Scan(&count)
	return count, err
}

func (s *PostgresStore) CountOnlineRuntimesByOrg(ctx context.Context, orgID string) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM runtimes WHERE org_id = $1 AND online = TRUE", orgID,
	).Scan(&count)
	return count, err
}
