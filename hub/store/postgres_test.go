package store

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
)

func newTestPostgresStore(t *testing.T) *PostgresStore {
	t.Helper()
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN not set, skipping Postgres tests")
	}
	s, err := NewPostgres(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// TestPostgresMigration verifies that migrations run without error on a fresh database.
func TestPostgresMigration(t *testing.T) {
	s := newTestPostgresStore(t)
	if err := s.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// TestPostgresFullFlow exercises the end-to-end flow that was failing in production:
// org creation -> runtime upsert -> agent upsert -> session creation.
func TestPostgresFullFlow(t *testing.T) {
	s := newTestPostgresStore(t)
	ctx := context.Background()

	orgID := "org_test_" + uuid.New().String()[:8]
	runtimeID := "runtime-" + uuid.New().String()[:8]
	agentID := "agent-" + uuid.New().String()[:8]
	userExternalID := "user_test_" + uuid.New().String()[:8]

	// 1. Create organization (idempotent)
	err := s.CreateOrganization(ctx, &Organization{
		ID: orgID, Name: orgID, Plan: "free", CreatedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("CreateOrganization: %v", err)
	}

	// Calling again should not error (ON CONFLICT DO NOTHING)
	err = s.CreateOrganization(ctx, &Organization{
		ID: orgID, Name: orgID, Plan: "free", CreatedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("CreateOrganization (idempotent): %v", err)
	}

	// 2. Upsert runtime with the org
	err = s.UpsertRuntime(ctx, &Runtime{
		ID: runtimeID, OrgID: orgID, Name: runtimeID, Online: true, LastSeen: time.Now(),
	})
	if err != nil {
		t.Fatalf("UpsertRuntime: %v", err)
	}

	// 3. Upsert agent
	err = s.UpsertAgent(ctx, &Agent{
		ID: agentID, OrgID: orgID, RuntimeID: runtimeID,
		Profile: "claude-code", Name: "Test Agent",
		Tags: "{}", Caps: "{}", Security: "{}",
	})
	if err != nil {
		t.Fatalf("UpsertAgent: %v", err)
	}

	// 4. Create session using external user ID (not UUID)
	// This is the actual flow: sessions.user_id stores the Clerk external ID.
	sessID := uuid.New().String()
	err = s.CreateSession(ctx, &Session{
		ID: sessID, OrgID: orgID, UserID: userExternalID,
		AgentID: agentID, RuntimeID: runtimeID,
		Profile: "claude-code", State: "creating",
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	// 5. Verify session was created
	sess, err := s.GetSession(ctx, sessID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if sess.UserID != userExternalID {
		t.Errorf("session user_id = %q, want %q", sess.UserID, userExternalID)
	}
	if sess.OrgID != orgID {
		t.Errorf("session org_id = %q, want %q", sess.OrgID, orgID)
	}

	// 6. List sessions by user (uses external ID)
	sessions, err := s.ListSessionsByUser(ctx, userExternalID)
	if err != nil {
		t.Fatalf("ListSessionsByUser: %v", err)
	}
	if len(sessions) != 1 {
		t.Errorf("got %d sessions, want 1", len(sessions))
	}

	// 7. Agent config override (was failing with wrong column name)
	override, err := s.GetAgentConfigOverride(ctx, agentID)
	if err != nil {
		t.Fatalf("GetAgentConfigOverride: %v", err)
	}
	if override != nil {
		t.Error("expected nil override for new agent")
	}

	// Cleanup
	_, _ = s.db.Exec("DELETE FROM messages WHERE session_id = $1", sessID)
	_, _ = s.db.Exec("DELETE FROM sessions WHERE id = $1", sessID)
	_, _ = s.db.Exec("DELETE FROM agents WHERE id = $1", agentID)
	_, _ = s.db.Exec("DELETE FROM runtimes WHERE id = $1", runtimeID)
	_, _ = s.db.Exec("DELETE FROM organizations WHERE id = $1", orgID)
}

// TestPostgresRuntimeReconnectOrgUpdate verifies that upserting a runtime with a
// new org_id correctly updates the record.
func TestPostgresRuntimeReconnectOrgUpdate(t *testing.T) {
	s := newTestPostgresStore(t)
	ctx := context.Background()

	runtimeID := "runtime-" + uuid.New().String()[:8]

	// Upsert with default org
	err := s.UpsertRuntime(ctx, &Runtime{
		ID: runtimeID, OrgID: "default", Name: runtimeID, Online: true, LastSeen: time.Now(),
	})
	if err != nil {
		t.Fatalf("UpsertRuntime (default): %v", err)
	}

	// Create a new org
	orgID := "org_test_" + uuid.New().String()[:8]
	_ = s.CreateOrganization(ctx, &Organization{
		ID: orgID, Name: orgID, Plan: "free", CreatedAt: time.Now(),
	})

	// Upsert again with new org
	err = s.UpsertRuntime(ctx, &Runtime{
		ID: runtimeID, OrgID: orgID, Name: runtimeID, Online: true, LastSeen: time.Now(),
	})
	if err != nil {
		t.Fatalf("UpsertRuntime (new org): %v", err)
	}

	// Verify org_id was updated
	rt, err := s.GetRuntime(ctx, runtimeID)
	if err != nil {
		t.Fatalf("GetRuntime: %v", err)
	}
	if rt.OrgID != orgID {
		t.Errorf("runtime org_id = %q, want %q", rt.OrgID, orgID)
	}

	// Cleanup
	_, _ = s.db.Exec("DELETE FROM runtimes WHERE id = $1", runtimeID)
	_, _ = s.db.Exec("DELETE FROM organizations WHERE id = $1", orgID)
}
