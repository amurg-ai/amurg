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
		`CREATE TABLE IF NOT EXISTS endpoints (
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
			user_id TEXT NOT NULL REFERENCES users(id),
			endpoint_id TEXT NOT NULL,
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
		`CREATE INDEX IF NOT EXISTS idx_endpoints_org_id ON endpoints(org_id)`,
		`CREATE TABLE IF NOT EXISTS endpoint_permissions (
			user_id TEXT NOT NULL,
			endpoint_id TEXT NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			PRIMARY KEY (user_id, endpoint_id)
		)`,
		`CREATE TABLE IF NOT EXISTS audit_events (
			id TEXT PRIMARY KEY,
			org_id TEXT NOT NULL DEFAULT 'default',
			action TEXT NOT NULL,
			user_id TEXT NOT NULL DEFAULT '',
			runtime_id TEXT NOT NULL DEFAULT '',
			session_id TEXT NOT NULL DEFAULT '',
			endpoint_id TEXT NOT NULL DEFAULT '',
			detail TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_audit_events_created_at ON audit_events(created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_audit_events_org_id ON audit_events(org_id)`,
		`CREATE INDEX IF NOT EXISTS idx_audit_events_action ON audit_events(action)`,
		`CREATE INDEX IF NOT EXISTS idx_audit_events_endpoint_id ON audit_events(endpoint_id)`,
		// Endpoint config overrides
		`CREATE TABLE IF NOT EXISTS endpoint_config_overrides (
			endpoint_id TEXT PRIMARY KEY,
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
		"INSERT INTO organizations (id, name, created_at) VALUES ($1, $2, $3)",
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
		 ON CONFLICT(id) DO UPDATE SET name=EXCLUDED.name, online=EXCLUDED.online, last_seen=EXCLUDED.last_seen`,
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

// --- Endpoints ---

func (s *PostgresStore) UpsertEndpoint(ctx context.Context, ep *Endpoint) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO endpoints (id, org_id, runtime_id, profile, name, tags, caps, security) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		 ON CONFLICT(id) DO UPDATE SET runtime_id=EXCLUDED.runtime_id, profile=EXCLUDED.profile, name=EXCLUDED.name, tags=EXCLUDED.tags, caps=EXCLUDED.caps, security=EXCLUDED.security`,
		ep.ID, ep.OrgID, ep.RuntimeID, ep.Profile, ep.Name, ep.Tags, ep.Caps, ep.Security,
	)
	return err
}

func (s *PostgresStore) GetEndpoint(ctx context.Context, id string) (*Endpoint, error) {
	var ep Endpoint
	err := s.db.QueryRowContext(ctx,
		"SELECT id, org_id, runtime_id, profile, name, tags, caps, security FROM endpoints WHERE id = $1", id,
	).Scan(&ep.ID, &ep.OrgID, &ep.RuntimeID, &ep.Profile, &ep.Name, &ep.Tags, &ep.Caps, &ep.Security)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &ep, err
}

func (s *PostgresStore) ListEndpoints(ctx context.Context, orgID string) ([]Endpoint, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT id, org_id, runtime_id, profile, name, tags, caps, security FROM endpoints WHERE org_id = $1 ORDER BY name",
		orgID,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var endpoints []Endpoint
	for rows.Next() {
		var ep Endpoint
		if err := rows.Scan(&ep.ID, &ep.OrgID, &ep.RuntimeID, &ep.Profile, &ep.Name, &ep.Tags, &ep.Caps, &ep.Security); err != nil {
			return nil, err
		}
		endpoints = append(endpoints, ep)
	}
	return endpoints, rows.Err()
}

func (s *PostgresStore) ListEndpointsByRuntime(ctx context.Context, runtimeID string) ([]Endpoint, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT id, org_id, runtime_id, profile, name, tags, caps, security FROM endpoints WHERE runtime_id = $1 ORDER BY name",
		runtimeID,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var endpoints []Endpoint
	for rows.Next() {
		var ep Endpoint
		if err := rows.Scan(&ep.ID, &ep.OrgID, &ep.RuntimeID, &ep.Profile, &ep.Name, &ep.Tags, &ep.Caps, &ep.Security); err != nil {
			return nil, err
		}
		endpoints = append(endpoints, ep)
	}
	return endpoints, rows.Err()
}

func (s *PostgresStore) DeleteEndpointsByRuntime(ctx context.Context, runtimeID string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM endpoints WHERE runtime_id = $1", runtimeID)
	return err
}

// --- Sessions ---

func (s *PostgresStore) CreateSession(ctx context.Context, sess *Session) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO sessions (id, org_id, user_id, endpoint_id, runtime_id, profile, state, native_handle, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		sess.ID, sess.OrgID, sess.UserID, sess.EndpointID, sess.RuntimeID, sess.Profile,
		sess.State, sess.NativeHandle, sess.CreatedAt, sess.UpdatedAt,
	)
	return err
}

func (s *PostgresStore) GetSession(ctx context.Context, id string) (*Session, error) {
	var sess Session
	err := s.db.QueryRowContext(ctx,
		`SELECT id, org_id, user_id, endpoint_id, runtime_id, profile, state, native_handle, created_at, updated_at
		 FROM sessions WHERE id = $1`, id,
	).Scan(&sess.ID, &sess.OrgID, &sess.UserID, &sess.EndpointID, &sess.RuntimeID, &sess.Profile,
		&sess.State, &sess.NativeHandle, &sess.CreatedAt, &sess.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &sess, err
}

func (s *PostgresStore) ListSessionsByUser(ctx context.Context, userID string) ([]Session, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT s.id, s.org_id, s.user_id, s.endpoint_id, s.runtime_id, s.profile, s.state, s.native_handle,
		        s.created_at, s.updated_at, COALESCE(e.name, '') as endpoint_name
		 FROM sessions s LEFT JOIN endpoints e ON s.endpoint_id = e.id
		 WHERE s.user_id = $1 ORDER BY s.updated_at DESC`, userID,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var sessions []Session
	for rows.Next() {
		var sess Session
		if err := rows.Scan(&sess.ID, &sess.OrgID, &sess.UserID, &sess.EndpointID, &sess.RuntimeID, &sess.Profile,
			&sess.State, &sess.NativeHandle, &sess.CreatedAt, &sess.UpdatedAt, &sess.EndpointName); err != nil {
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
			`SELECT id, org_id, user_id, endpoint_id, runtime_id, profile, state, native_handle, created_at, updated_at
			 FROM sessions WHERE state NOT IN ('closed') ORDER BY updated_at DESC`)
	} else {
		rows, err = s.db.QueryContext(ctx,
			`SELECT id, org_id, user_id, endpoint_id, runtime_id, profile, state, native_handle, created_at, updated_at
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
		if err := rows.Scan(&sess.ID, &sess.OrgID, &sess.UserID, &sess.EndpointID, &sess.RuntimeID, &sess.Profile,
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

// --- Endpoint Permissions ---

func (s *PostgresStore) GrantEndpointAccess(ctx context.Context, userID, endpointID string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO endpoint_permissions (user_id, endpoint_id, created_at) VALUES ($1, $2, $3)
		 ON CONFLICT(user_id, endpoint_id) DO NOTHING`,
		userID, endpointID, time.Now(),
	)
	return err
}

func (s *PostgresStore) RevokeEndpointAccess(ctx context.Context, userID, endpointID string) error {
	_, err := s.db.ExecContext(ctx,
		"DELETE FROM endpoint_permissions WHERE user_id = $1 AND endpoint_id = $2",
		userID, endpointID,
	)
	return err
}

func (s *PostgresStore) ListUserEndpoints(ctx context.Context, userID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT endpoint_id FROM endpoint_permissions WHERE user_id = $1", userID,
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

func (s *PostgresStore) HasEndpointAccess(ctx context.Context, userID, endpointID string) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM endpoint_permissions WHERE user_id = $1 AND endpoint_id = $2",
		userID, endpointID,
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
		`INSERT INTO audit_events (id, org_id, action, user_id, runtime_id, session_id, endpoint_id, detail, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		event.ID, event.OrgID, event.Action, event.UserID, event.RuntimeID, event.SessionID, event.EndpointID, detail, event.CreatedAt,
	)
	return err
}

func (s *PostgresStore) ListAuditEvents(ctx context.Context, orgID string, limit, offset int) ([]AuditEvent, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, org_id, action, user_id, runtime_id, session_id, endpoint_id, detail, created_at
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
		if err := rows.Scan(&e.ID, &e.OrgID, &e.Action, &e.UserID, &e.RuntimeID, &e.SessionID, &e.EndpointID, &detail, &e.CreatedAt); err != nil {
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
	query := `SELECT id, org_id, action, user_id, runtime_id, session_id, endpoint_id, detail, created_at
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
	if filter.EndpointID != "" {
		query += fmt.Sprintf(" AND endpoint_id = $%d", argN)
		args = append(args, filter.EndpointID)
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
		if err := rows.Scan(&e.ID, &e.OrgID, &e.Action, &e.UserID, &e.RuntimeID, &e.SessionID, &e.EndpointID, &detail, &e.CreatedAt); err != nil {
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
		`SELECT s.id, s.org_id, s.user_id, s.endpoint_id, s.runtime_id, s.profile, s.state, s.native_handle,
		        s.created_at, s.updated_at, COALESCE(e.name, '') as endpoint_name
		 FROM sessions s LEFT JOIN endpoints e ON s.endpoint_id = e.id
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
		if err := rows.Scan(&sess.ID, &sess.OrgID, &sess.UserID, &sess.EndpointID, &sess.RuntimeID, &sess.Profile,
			&sess.State, &sess.NativeHandle, &sess.CreatedAt, &sess.UpdatedAt, &sess.EndpointName); err != nil {
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

// --- Endpoint Config Overrides ---

func (s *PostgresStore) UpsertEndpointConfigOverride(ctx context.Context, override *EndpointConfigOverride) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO endpoint_config_overrides (endpoint_id, org_id, security, limits, updated_by, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 ON CONFLICT(endpoint_id) DO UPDATE SET
		   security = EXCLUDED.security,
		   limits = EXCLUDED.limits,
		   updated_by = EXCLUDED.updated_by,
		   updated_at = EXCLUDED.updated_at`,
		override.EndpointID, override.OrgID, override.Security, override.Limits,
		override.UpdatedBy, override.UpdatedAt,
	)
	return err
}

func (s *PostgresStore) GetEndpointConfigOverride(ctx context.Context, endpointID string) (*EndpointConfigOverride, error) {
	var o EndpointConfigOverride
	err := s.db.QueryRowContext(ctx,
		"SELECT endpoint_id, org_id, security, limits, updated_by, updated_at FROM endpoint_config_overrides WHERE endpoint_id = $1",
		endpointID,
	).Scan(&o.EndpointID, &o.OrgID, &o.Security, &o.Limits, &o.UpdatedBy, &o.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &o, err
}

func (s *PostgresStore) ListEndpointConfigOverrides(ctx context.Context, orgID string) ([]EndpointConfigOverride, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT endpoint_id, org_id, security, limits, updated_by, updated_at FROM endpoint_config_overrides WHERE org_id = $1",
		orgID,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var overrides []EndpointConfigOverride
	for rows.Next() {
		var o EndpointConfigOverride
		if err := rows.Scan(&o.EndpointID, &o.OrgID, &o.Security, &o.Limits, &o.UpdatedBy, &o.UpdatedAt); err != nil {
			return nil, err
		}
		overrides = append(overrides, o)
	}
	return overrides, rows.Err()
}

func (s *PostgresStore) DeleteEndpointConfigOverride(ctx context.Context, endpointID string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM endpoint_config_overrides WHERE endpoint_id = $1", endpointID)
	return err
}
