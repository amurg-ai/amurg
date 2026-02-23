package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// SQLiteStore implements Store using SQLite.
type SQLiteStore struct {
	db *sql.DB
}

// NewSQLite creates a new SQLite store and runs migrations.
func NewSQLite(dsn string) (*SQLiteStore, error) {
	// For in-memory databases, use shared cache so all connections in the pool
	// see the same data. Without this, each pooled connection gets a separate
	// empty database.
	if dsn == ":memory:" {
		dsn = "file::memory:?cache=shared"
	}

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	// Enable WAL mode for better concurrent read/write.
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("set WAL mode: %w", err)
	}
	if _, err := db.Exec("PRAGMA foreign_keys=ON"); err != nil {
		db.Close()
		return nil, fmt.Errorf("enable foreign keys: %w", err)
	}

	s := &SQLiteStore{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}

	return s, nil
}

func (s *SQLiteStore) addColumnIfNotExists(table, column, definition string) error {
	_, err := s.db.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, column, definition))
	if err != nil && strings.Contains(err.Error(), "duplicate column") {
		return nil
	}
	return err
}

func (s *SQLiteStore) migrate() error {
	migrations := []string{
		`CREATE TABLE IF NOT EXISTS users (
			id TEXT PRIMARY KEY,
			username TEXT UNIQUE NOT NULL,
			password_hash TEXT NOT NULL,
			role TEXT NOT NULL DEFAULT 'user',
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS runtimes (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL DEFAULT '',
			online INTEGER NOT NULL DEFAULT 0,
			last_seen DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS endpoints (
			id TEXT PRIMARY KEY,
			runtime_id TEXT NOT NULL REFERENCES runtimes(id),
			profile TEXT NOT NULL,
			name TEXT NOT NULL DEFAULT '',
			tags TEXT NOT NULL DEFAULT '{}',
			caps TEXT NOT NULL DEFAULT '{}',
			security TEXT NOT NULL DEFAULT '{}'
		)`,
		`CREATE TABLE IF NOT EXISTS sessions (
			id TEXT PRIMARY KEY,
			user_id TEXT NOT NULL REFERENCES users(id),
			endpoint_id TEXT NOT NULL,
			runtime_id TEXT NOT NULL,
			profile TEXT NOT NULL,
			state TEXT NOT NULL DEFAULT 'active',
			native_handle TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS messages (
			id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL REFERENCES sessions(id),
			seq INTEGER NOT NULL,
			direction TEXT NOT NULL,
			channel TEXT NOT NULL DEFAULT 'stdout',
			content TEXT NOT NULL,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_messages_session_seq ON messages(session_id, seq)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_session_id ON messages(session_id)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_user_id ON sessions(user_id)`,
		`CREATE TABLE IF NOT EXISTS endpoint_permissions (
			user_id TEXT NOT NULL,
			endpoint_id TEXT NOT NULL,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (user_id, endpoint_id)
		)`,
		`CREATE TABLE IF NOT EXISTS audit_events (
			id TEXT PRIMARY KEY,
			action TEXT NOT NULL,
			user_id TEXT NOT NULL DEFAULT '',
			runtime_id TEXT NOT NULL DEFAULT '',
			session_id TEXT NOT NULL DEFAULT '',
			endpoint_id TEXT NOT NULL DEFAULT '',
			detail TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_audit_events_created_at ON audit_events(created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_audit_events_action ON audit_events(action)`,
		`CREATE INDEX IF NOT EXISTS idx_audit_events_endpoint_id ON audit_events(endpoint_id)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_state ON sessions(state)`,

		// Phase 3: organizations table
		`CREATE TABLE IF NOT EXISTS organizations (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`INSERT OR IGNORE INTO organizations (id, name) VALUES ('default', 'Default')`,
	}

	for _, m := range migrations {
		if _, err := s.db.Exec(m); err != nil {
			return fmt.Errorf("migration failed: %w\n  SQL: %s", err, m)
		}
	}

	// Phase 3: add org_id columns to existing tables.
	// SQLite doesn't support ADD COLUMN IF NOT EXISTS, so we ignore duplicate column errors.
	columnMigrations := []struct {
		table, column, definition string
	}{
		{"users", "org_id", "TEXT NOT NULL DEFAULT 'default'"},
		{"users", "external_id", "TEXT NOT NULL DEFAULT ''"},
		{"runtimes", "org_id", "TEXT NOT NULL DEFAULT 'default'"},
		{"endpoints", "org_id", "TEXT NOT NULL DEFAULT 'default'"},
		{"sessions", "org_id", "TEXT NOT NULL DEFAULT 'default'"},
		{"audit_events", "org_id", "TEXT NOT NULL DEFAULT 'default'"},
		{"audit_events", "endpoint_id", "TEXT NOT NULL DEFAULT ''"},
		{"endpoints", "security", "TEXT NOT NULL DEFAULT '{}'"},
	}
	for _, cm := range columnMigrations {
		if err := s.addColumnIfNotExists(cm.table, cm.column, cm.definition); err != nil {
			return fmt.Errorf("add column %s.%s: %w", cm.table, cm.column, err)
		}
	}

	// Phase 3: indexes on org_id
	orgIndexes := []string{
		`CREATE INDEX IF NOT EXISTS idx_users_org_id ON users(org_id)`,
		`CREATE INDEX IF NOT EXISTS idx_runtimes_org_id ON runtimes(org_id)`,
		`CREATE INDEX IF NOT EXISTS idx_endpoints_org_id ON endpoints(org_id)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_org_id ON sessions(org_id)`,
		`CREATE INDEX IF NOT EXISTS idx_audit_events_org_id ON audit_events(org_id)`,
	}
	for _, idx := range orgIndexes {
		if _, err := s.db.Exec(idx); err != nil {
			return fmt.Errorf("migration failed: %w\n  SQL: %s", err, idx)
		}
	}

	// Endpoint config overrides table.
	configOverrideMigrations := []string{
		`CREATE TABLE IF NOT EXISTS endpoint_config_overrides (
			endpoint_id TEXT PRIMARY KEY,
			org_id TEXT NOT NULL DEFAULT 'default',
			security TEXT NOT NULL DEFAULT '{}',
			limits TEXT NOT NULL DEFAULT '{}',
			updated_by TEXT NOT NULL DEFAULT '',
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
	}
	for _, m := range configOverrideMigrations {
		if _, err := s.db.Exec(m); err != nil {
			return fmt.Errorf("migration failed: %w\n  SQL: %s", err, m)
		}
	}

	return nil
}

func (s *SQLiteStore) Ping(ctx context.Context) error {
	return s.db.PingContext(ctx)
}

func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

// --- Organizations ---

func (s *SQLiteStore) CreateOrganization(ctx context.Context, org *Organization) error {
	_, err := s.db.ExecContext(ctx,
		"INSERT INTO organizations (id, name, created_at) VALUES (?, ?, ?)",
		org.ID, org.Name, org.CreatedAt)
	return err
}

func (s *SQLiteStore) GetOrganization(ctx context.Context, id string) (*Organization, error) {
	var org Organization
	err := s.db.QueryRowContext(ctx,
		"SELECT id, name, created_at FROM organizations WHERE id = ?", id,
	).Scan(&org.ID, &org.Name, &org.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &org, err
}

// --- Users ---

func (s *SQLiteStore) CreateUser(ctx context.Context, user *User) error {
	_, err := s.db.ExecContext(ctx,
		"INSERT INTO users (id, org_id, external_id, username, password_hash, role, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)",
		user.ID, user.OrgID, user.ExternalID, user.Username, user.PasswordHash, user.Role, user.CreatedAt,
	)
	return err
}

func (s *SQLiteStore) GetUser(ctx context.Context, orgID, username string) (*User, error) {
	var u User
	err := s.db.QueryRowContext(ctx,
		"SELECT id, org_id, external_id, username, password_hash, role, created_at FROM users WHERE org_id = ? AND username = ?",
		orgID, username,
	).Scan(&u.ID, &u.OrgID, &u.ExternalID, &u.Username, &u.PasswordHash, &u.Role, &u.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &u, err
}

func (s *SQLiteStore) GetUserByID(ctx context.Context, id string) (*User, error) {
	var u User
	err := s.db.QueryRowContext(ctx,
		"SELECT id, org_id, external_id, username, password_hash, role, created_at FROM users WHERE id = ?", id,
	).Scan(&u.ID, &u.OrgID, &u.ExternalID, &u.Username, &u.PasswordHash, &u.Role, &u.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &u, err
}

func (s *SQLiteStore) GetUserByExternalID(ctx context.Context, externalID string) (*User, error) {
	var u User
	err := s.db.QueryRowContext(ctx,
		"SELECT id, org_id, external_id, username, password_hash, role, created_at FROM users WHERE external_id = ?",
		externalID,
	).Scan(&u.ID, &u.OrgID, &u.ExternalID, &u.Username, &u.PasswordHash, &u.Role, &u.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &u, err
}

func (s *SQLiteStore) ListUsers(ctx context.Context, orgID string) ([]User, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT id, org_id, external_id, username, role, created_at FROM users WHERE org_id = ? ORDER BY created_at",
		orgID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.OrgID, &u.ExternalID, &u.Username, &u.Role, &u.CreatedAt); err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

// --- Runtimes ---

func (s *SQLiteStore) UpsertRuntime(ctx context.Context, rt *Runtime) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO runtimes (id, org_id, name, online, last_seen) VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET name=excluded.name, online=excluded.online, last_seen=excluded.last_seen`,
		rt.ID, rt.OrgID, rt.Name, rt.Online, rt.LastSeen,
	)
	return err
}

func (s *SQLiteStore) GetRuntime(ctx context.Context, id string) (*Runtime, error) {
	var rt Runtime
	err := s.db.QueryRowContext(ctx,
		"SELECT id, org_id, name, online, last_seen FROM runtimes WHERE id = ?", id,
	).Scan(&rt.ID, &rt.OrgID, &rt.Name, &rt.Online, &rt.LastSeen)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &rt, err
}

func (s *SQLiteStore) ListRuntimes(ctx context.Context, orgID string) ([]Runtime, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT id, org_id, name, online, last_seen FROM runtimes WHERE org_id = ? ORDER BY name",
		orgID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

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

func (s *SQLiteStore) SetRuntimeOnline(ctx context.Context, id string, online bool) error {
	_, err := s.db.ExecContext(ctx,
		"UPDATE runtimes SET online = ?, last_seen = ? WHERE id = ?",
		online, time.Now(), id,
	)
	return err
}

// --- Endpoints ---

func (s *SQLiteStore) UpsertEndpoint(ctx context.Context, ep *Endpoint) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO endpoints (id, org_id, runtime_id, profile, name, tags, caps, security) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET runtime_id=excluded.runtime_id, profile=excluded.profile, name=excluded.name, tags=excluded.tags, caps=excluded.caps, security=excluded.security`,
		ep.ID, ep.OrgID, ep.RuntimeID, ep.Profile, ep.Name, ep.Tags, ep.Caps, ep.Security,
	)
	return err
}

func (s *SQLiteStore) GetEndpoint(ctx context.Context, id string) (*Endpoint, error) {
	var ep Endpoint
	err := s.db.QueryRowContext(ctx,
		"SELECT id, org_id, runtime_id, profile, name, tags, caps, security FROM endpoints WHERE id = ?", id,
	).Scan(&ep.ID, &ep.OrgID, &ep.RuntimeID, &ep.Profile, &ep.Name, &ep.Tags, &ep.Caps, &ep.Security)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &ep, err
}

func (s *SQLiteStore) ListEndpoints(ctx context.Context, orgID string) ([]Endpoint, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT id, org_id, runtime_id, profile, name, tags, caps, security FROM endpoints WHERE org_id = ? ORDER BY name",
		orgID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

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

func (s *SQLiteStore) ListEndpointsByRuntime(ctx context.Context, runtimeID string) ([]Endpoint, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT id, org_id, runtime_id, profile, name, tags, caps, security FROM endpoints WHERE runtime_id = ? ORDER BY name",
		runtimeID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

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

func (s *SQLiteStore) DeleteEndpointsByRuntime(ctx context.Context, runtimeID string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM endpoints WHERE runtime_id = ?", runtimeID)
	return err
}

// --- Sessions ---

func (s *SQLiteStore) CreateSession(ctx context.Context, sess *Session) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO sessions (id, org_id, user_id, endpoint_id, runtime_id, profile, state, native_handle, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sess.ID, sess.OrgID, sess.UserID, sess.EndpointID, sess.RuntimeID, sess.Profile,
		sess.State, sess.NativeHandle, sess.CreatedAt, sess.UpdatedAt,
	)
	return err
}

func (s *SQLiteStore) GetSession(ctx context.Context, id string) (*Session, error) {
	var sess Session
	err := s.db.QueryRowContext(ctx,
		`SELECT id, org_id, user_id, endpoint_id, runtime_id, profile, state, native_handle, created_at, updated_at
		 FROM sessions WHERE id = ?`, id,
	).Scan(&sess.ID, &sess.OrgID, &sess.UserID, &sess.EndpointID, &sess.RuntimeID, &sess.Profile,
		&sess.State, &sess.NativeHandle, &sess.CreatedAt, &sess.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &sess, err
}

func (s *SQLiteStore) ListSessionsByUser(ctx context.Context, userID string) ([]Session, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT s.id, s.org_id, s.user_id, s.endpoint_id, s.runtime_id, s.profile, s.state, s.native_handle,
		        s.created_at, s.updated_at, COALESCE(e.name, '') as endpoint_name
		 FROM sessions s LEFT JOIN endpoints e ON s.endpoint_id = e.id
		 WHERE s.user_id = ? ORDER BY s.updated_at DESC`, userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

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

func (s *SQLiteStore) UpdateSessionState(ctx context.Context, id string, state string) error {
	_, err := s.db.ExecContext(ctx,
		"UPDATE sessions SET state = ?, updated_at = ? WHERE id = ?",
		state, time.Now(), id,
	)
	return err
}

func (s *SQLiteStore) SetSessionNativeHandle(ctx context.Context, id, handle string) error {
	_, err := s.db.ExecContext(ctx,
		"UPDATE sessions SET native_handle = ?, updated_at = ? WHERE id = ?",
		handle, time.Now(), id,
	)
	return err
}

// --- Messages ---

func (s *SQLiteStore) AppendMessage(ctx context.Context, msg *Message) (int64, error) {
	var seq int64
	err := s.db.QueryRowContext(ctx,
		`INSERT INTO messages (id, session_id, seq, direction, channel, content, created_at)
		 VALUES (?, ?, (SELECT COALESCE(MAX(seq),0)+1 FROM messages WHERE session_id = ?), ?, ?, ?, ?)
		 RETURNING seq`,
		msg.ID, msg.SessionID, msg.SessionID, msg.Direction, msg.Channel, msg.Content, msg.CreatedAt,
	).Scan(&seq)
	return seq, err
}

func (s *SQLiteStore) GetMessages(ctx context.Context, sessionID string, afterSeq int64, limit int) ([]Message, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, session_id, seq, direction, channel, content, created_at
		 FROM messages WHERE session_id = ? AND seq > ? ORDER BY seq LIMIT ?`,
		sessionID, afterSeq, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

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

func (s *SQLiteStore) MessageExists(ctx context.Context, sessionID, messageID string) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM messages WHERE session_id = ? AND id = ?", sessionID, messageID,
	).Scan(&count)
	return count > 0, err
}

// --- Sessions (additional) ---

func (s *SQLiteStore) ListActiveSessions(ctx context.Context, orgID string) ([]Session, error) {
	var rows *sql.Rows
	var err error
	if orgID == "" {
		rows, err = s.db.QueryContext(ctx,
			`SELECT id, org_id, user_id, endpoint_id, runtime_id, profile, state, native_handle, created_at, updated_at
			 FROM sessions WHERE state NOT IN ('closed') ORDER BY updated_at DESC`)
	} else {
		rows, err = s.db.QueryContext(ctx,
			`SELECT id, org_id, user_id, endpoint_id, runtime_id, profile, state, native_handle, created_at, updated_at
			 FROM sessions WHERE org_id = ? AND state NOT IN ('closed') ORDER BY updated_at DESC`,
			orgID)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

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

func (s *SQLiteStore) CountActiveSessionsByUser(ctx context.Context, userID string) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM sessions WHERE user_id = ? AND state NOT IN ('closed')", userID,
	).Scan(&count)
	return count, err
}

// --- Endpoint Permissions ---

func (s *SQLiteStore) GrantEndpointAccess(ctx context.Context, userID, endpointID string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO endpoint_permissions (user_id, endpoint_id, created_at) VALUES (?, ?, ?)
		 ON CONFLICT(user_id, endpoint_id) DO NOTHING`,
		userID, endpointID, time.Now(),
	)
	return err
}

func (s *SQLiteStore) RevokeEndpointAccess(ctx context.Context, userID, endpointID string) error {
	_, err := s.db.ExecContext(ctx,
		"DELETE FROM endpoint_permissions WHERE user_id = ? AND endpoint_id = ?",
		userID, endpointID,
	)
	return err
}

func (s *SQLiteStore) ListUserEndpoints(ctx context.Context, userID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT endpoint_id FROM endpoint_permissions WHERE user_id = ?", userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

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

func (s *SQLiteStore) HasEndpointAccess(ctx context.Context, userID, endpointID string) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM endpoint_permissions WHERE user_id = ? AND endpoint_id = ?",
		userID, endpointID,
	).Scan(&count)
	return count > 0, err
}

// --- Audit ---

func (s *SQLiteStore) LogAuditEvent(ctx context.Context, event *AuditEvent) error {
	detail := ""
	if event.Detail != nil {
		detail = string(event.Detail)
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO audit_events (id, org_id, action, user_id, runtime_id, session_id, endpoint_id, detail, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		event.ID, event.OrgID, event.Action, event.UserID, event.RuntimeID, event.SessionID, event.EndpointID, detail, event.CreatedAt,
	)
	return err
}

func (s *SQLiteStore) ListAuditEvents(ctx context.Context, orgID string, limit, offset int) ([]AuditEvent, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, org_id, action, user_id, runtime_id, session_id, endpoint_id, detail, created_at
		 FROM audit_events WHERE org_id = ? ORDER BY created_at DESC LIMIT ? OFFSET ?`,
		orgID, limit, offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

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

func (s *SQLiteStore) ListAuditEventsFiltered(ctx context.Context, orgID string, filter AuditFilter) ([]AuditEvent, error) {
	query := `SELECT id, org_id, action, user_id, runtime_id, session_id, endpoint_id, detail, created_at
	          FROM audit_events WHERE org_id = ?`
	args := []any{orgID}

	if filter.Action != "" {
		query += " AND action LIKE ?"
		args = append(args, filter.Action+"%")
	}
	if filter.UserID != "" {
		query += " AND user_id = ?"
		args = append(args, filter.UserID)
	}
	if filter.SessionID != "" {
		query += " AND session_id = ?"
		args = append(args, filter.SessionID)
	}
	if filter.EndpointID != "" {
		query += " AND endpoint_id = ?"
		args = append(args, filter.EndpointID)
	}

	query += " ORDER BY created_at DESC"

	limit := filter.Limit
	if limit <= 0 {
		limit = 50
	}
	query += " LIMIT ?"
	args = append(args, limit)

	if filter.Offset > 0 {
		query += " OFFSET ?"
		args = append(args, filter.Offset)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

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

func (s *SQLiteStore) ListAllSessions(ctx context.Context, orgID string) ([]Session, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT s.id, s.org_id, s.user_id, s.endpoint_id, s.runtime_id, s.profile, s.state, s.native_handle,
		        s.created_at, s.updated_at, COALESCE(e.name, '') as endpoint_name
		 FROM sessions s LEFT JOIN endpoints e ON s.endpoint_id = e.id
		 WHERE s.org_id = ?
		 ORDER BY s.updated_at DESC`,
		orgID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

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

func (s *SQLiteStore) PurgeOldMessages(ctx context.Context, before time.Time) (int64, error) {
	result, err := s.db.ExecContext(ctx,
		"DELETE FROM messages WHERE created_at < ?", before,
	)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (s *SQLiteStore) PurgeOldAuditEvents(ctx context.Context, before time.Time) (int64, error) {
	result, err := s.db.ExecContext(ctx,
		"DELETE FROM audit_events WHERE created_at < ?", before,
	)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// --- Endpoint Config Overrides ---

func (s *SQLiteStore) UpsertEndpointConfigOverride(ctx context.Context, override *EndpointConfigOverride) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO endpoint_config_overrides (endpoint_id, org_id, security, limits, updated_by, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(endpoint_id) DO UPDATE SET
		   security = excluded.security,
		   limits = excluded.limits,
		   updated_by = excluded.updated_by,
		   updated_at = excluded.updated_at`,
		override.EndpointID, override.OrgID, override.Security, override.Limits,
		override.UpdatedBy, override.UpdatedAt,
	)
	return err
}

func (s *SQLiteStore) GetEndpointConfigOverride(ctx context.Context, endpointID string) (*EndpointConfigOverride, error) {
	var o EndpointConfigOverride
	err := s.db.QueryRowContext(ctx,
		"SELECT endpoint_id, org_id, security, limits, updated_by, updated_at FROM endpoint_config_overrides WHERE endpoint_id = ?",
		endpointID,
	).Scan(&o.EndpointID, &o.OrgID, &o.Security, &o.Limits, &o.UpdatedBy, &o.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &o, err
}

func (s *SQLiteStore) ListEndpointConfigOverrides(ctx context.Context, orgID string) ([]EndpointConfigOverride, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT endpoint_id, org_id, security, limits, updated_by, updated_at FROM endpoint_config_overrides WHERE org_id = ?",
		orgID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

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

func (s *SQLiteStore) DeleteEndpointConfigOverride(ctx context.Context, endpointID string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM endpoint_config_overrides WHERE endpoint_id = ?", endpointID)
	return err
}
