package store

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
)

func newTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	s, err := NewSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// createTestUser is a helper that inserts a user and returns it.
func createTestUser(t *testing.T, s *SQLiteStore, username, role string) *User {
	t.Helper()
	u := &User{
		ID:           uuid.New().String(),
		OrgID:        "default",
		Username:     username,
		PasswordHash: "hash-" + username,
		Role:         role,
		CreatedAt:    time.Now(),
	}
	if err := s.CreateUser(context.Background(), u); err != nil {
		t.Fatalf("createTestUser(%s): %v", username, err)
	}
	return u
}

// createTestRuntime is a helper that inserts a runtime and returns it.
func createTestRuntime(t *testing.T, s *SQLiteStore, name string) *Runtime {
	t.Helper()
	rt := &Runtime{
		ID:       uuid.New().String(),
		OrgID:    "default",
		Name:     name,
		Online:   true,
		LastSeen: time.Now(),
	}
	if err := s.UpsertRuntime(context.Background(), rt); err != nil {
		t.Fatalf("createTestRuntime(%s): %v", name, err)
	}
	return rt
}

// createTestEndpoint is a helper that inserts an endpoint and returns it.
func createTestEndpoint(t *testing.T, s *SQLiteStore, runtimeID, name string) *Endpoint {
	t.Helper()
	ep := &Endpoint{
		ID:        uuid.New().String(),
		OrgID:     "default",
		RuntimeID: runtimeID,
		Profile:   "test-profile",
		Name:      name,
		Tags:      `{"env":"test"}`,
		Caps:      `{"streaming":true}`,
		Security:  "{}",
	}
	if err := s.UpsertEndpoint(context.Background(), ep); err != nil {
		t.Fatalf("createTestEndpoint(%s): %v", name, err)
	}
	return ep
}

// createTestSession is a helper that inserts a session and returns it.
func createTestSession(t *testing.T, s *SQLiteStore, userID, endpointID, runtimeID, state string) *Session {
	t.Helper()
	sess := &Session{
		ID:         uuid.New().String(),
		OrgID:      "default",
		UserID:     userID,
		EndpointID: endpointID,
		RuntimeID:  runtimeID,
		Profile:    "test-profile",
		State:      state,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}
	if err := s.CreateSession(context.Background(), sess); err != nil {
		t.Fatalf("createTestSession: %v", err)
	}
	return sess
}

func TestCreateAndGetUser(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	user := &User{
		ID:           uuid.New().String(),
		OrgID:        "default",
		Username:     "alice",
		PasswordHash: "hashed-pw",
		Role:         "admin",
		CreatedAt:    time.Now(),
	}

	if err := s.CreateUser(ctx, user); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	// Get by username
	got, err := s.GetUser(ctx, "default", "alice")
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if got == nil {
		t.Fatal("GetUser returned nil")
	}
	if got.ID != user.ID {
		t.Errorf("ID: got %q, want %q", got.ID, user.ID)
	}
	if got.Username != "alice" {
		t.Errorf("Username: got %q, want %q", got.Username, "alice")
	}
	if got.PasswordHash != "hashed-pw" {
		t.Errorf("PasswordHash: got %q, want %q", got.PasswordHash, "hashed-pw")
	}
	if got.Role != "admin" {
		t.Errorf("Role: got %q, want %q", got.Role, "admin")
	}

	// Get by ID
	gotByID, err := s.GetUserByID(ctx, user.ID)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	if gotByID == nil {
		t.Fatal("GetUserByID returned nil")
	}
	if gotByID.Username != "alice" {
		t.Errorf("GetUserByID Username: got %q, want %q", gotByID.Username, "alice")
	}

	// Nonexistent user returns nil, not error
	missing, err := s.GetUser(ctx, "default", "nobody")
	if err != nil {
		t.Fatalf("GetUser(nobody): %v", err)
	}
	if missing != nil {
		t.Errorf("expected nil for nonexistent user, got %+v", missing)
	}
}

func TestListUsers(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	createTestUser(t, s, "alice", "admin")
	createTestUser(t, s, "bob", "user")
	createTestUser(t, s, "charlie", "user")

	users, err := s.ListUsers(ctx, "default")
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	if len(users) != 3 {
		t.Fatalf("ListUsers: got %d users, want 3", len(users))
	}
}

func TestDuplicateUser(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	createTestUser(t, s, "alice", "admin")

	dup := &User{
		ID:           uuid.New().String(),
		OrgID:        "default",
		Username:     "alice",
		PasswordHash: "other-hash",
		Role:         "user",
		CreatedAt:    time.Now(),
	}
	err := s.CreateUser(ctx, dup)
	if err == nil {
		t.Fatal("expected error creating duplicate user, got nil")
	}
}

func TestUpsertRuntime(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	rt := &Runtime{
		ID:       "rt-1",
		OrgID:    "default",
		Name:     "runtime-alpha",
		Online:   true,
		LastSeen: time.Now(),
	}

	if err := s.UpsertRuntime(ctx, rt); err != nil {
		t.Fatalf("UpsertRuntime: %v", err)
	}

	got, err := s.GetRuntime(ctx, "rt-1")
	if err != nil {
		t.Fatalf("GetRuntime: %v", err)
	}
	if got == nil {
		t.Fatal("GetRuntime returned nil")
	}
	if got.Name != "runtime-alpha" {
		t.Errorf("Name: got %q, want %q", got.Name, "runtime-alpha")
	}
	if !got.Online {
		t.Error("Online: got false, want true")
	}

	// Upsert again with changed name
	rt.Name = "runtime-beta"
	if err := s.UpsertRuntime(ctx, rt); err != nil {
		t.Fatalf("UpsertRuntime (update): %v", err)
	}
	got, err = s.GetRuntime(ctx, "rt-1")
	if err != nil {
		t.Fatalf("GetRuntime after update: %v", err)
	}
	if got.Name != "runtime-beta" {
		t.Errorf("Name after upsert: got %q, want %q", got.Name, "runtime-beta")
	}
}

func TestSetRuntimeOnline(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	rt := createTestRuntime(t, s, "runtime-1")

	// Should be online after creation
	got, _ := s.GetRuntime(ctx, rt.ID)
	if !got.Online {
		t.Error("expected online after create")
	}

	// Toggle offline
	if err := s.SetRuntimeOnline(ctx, rt.ID, false); err != nil {
		t.Fatalf("SetRuntimeOnline(false): %v", err)
	}
	got, _ = s.GetRuntime(ctx, rt.ID)
	if got.Online {
		t.Error("expected offline after SetRuntimeOnline(false)")
	}

	// Toggle back online
	if err := s.SetRuntimeOnline(ctx, rt.ID, true); err != nil {
		t.Fatalf("SetRuntimeOnline(true): %v", err)
	}
	got, _ = s.GetRuntime(ctx, rt.ID)
	if !got.Online {
		t.Error("expected online after SetRuntimeOnline(true)")
	}
}

func TestListRuntimes(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	createTestRuntime(t, s, "alpha")
	createTestRuntime(t, s, "beta")
	createTestRuntime(t, s, "gamma")

	runtimes, err := s.ListRuntimes(ctx, "default")
	if err != nil {
		t.Fatalf("ListRuntimes: %v", err)
	}
	if len(runtimes) != 3 {
		t.Fatalf("ListRuntimes: got %d, want 3", len(runtimes))
	}
}

func TestUpsertEndpoint(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	rt := createTestRuntime(t, s, "runtime-1")

	ep := &Endpoint{
		ID:        "ep-1",
		OrgID:     "default",
		RuntimeID: rt.ID,
		Profile:   "chat-v1",
		Name:      "Chat Agent",
		Tags:      `{"env":"prod"}`,
		Caps:      `{"streaming":true}`,
		Security:  "{}",
	}

	if err := s.UpsertEndpoint(ctx, ep); err != nil {
		t.Fatalf("UpsertEndpoint: %v", err)
	}

	got, err := s.GetEndpoint(ctx, "ep-1")
	if err != nil {
		t.Fatalf("GetEndpoint: %v", err)
	}
	if got == nil {
		t.Fatal("GetEndpoint returned nil")
	}
	if got.RuntimeID != rt.ID {
		t.Errorf("RuntimeID: got %q, want %q", got.RuntimeID, rt.ID)
	}
	if got.Profile != "chat-v1" {
		t.Errorf("Profile: got %q, want %q", got.Profile, "chat-v1")
	}
	if got.Name != "Chat Agent" {
		t.Errorf("Name: got %q, want %q", got.Name, "Chat Agent")
	}
	if got.Tags != `{"env":"prod"}` {
		t.Errorf("Tags: got %q, want %q", got.Tags, `{"env":"prod"}`)
	}
	if got.Caps != `{"streaming":true}` {
		t.Errorf("Caps: got %q, want %q", got.Caps, `{"streaming":true}`)
	}
}

func TestListEndpoints(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	rt := createTestRuntime(t, s, "runtime-1")
	createTestEndpoint(t, s, rt.ID, "ep-a")
	createTestEndpoint(t, s, rt.ID, "ep-b")

	eps, err := s.ListEndpoints(ctx, "default")
	if err != nil {
		t.Fatalf("ListEndpoints: %v", err)
	}
	if len(eps) != 2 {
		t.Fatalf("ListEndpoints: got %d, want 2", len(eps))
	}
}

func TestListEndpointsByRuntime(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	rt1 := createTestRuntime(t, s, "runtime-1")
	rt2 := createTestRuntime(t, s, "runtime-2")
	createTestEndpoint(t, s, rt1.ID, "ep-a")
	createTestEndpoint(t, s, rt1.ID, "ep-b")
	createTestEndpoint(t, s, rt2.ID, "ep-c")

	eps, err := s.ListEndpointsByRuntime(ctx, rt1.ID)
	if err != nil {
		t.Fatalf("ListEndpointsByRuntime: %v", err)
	}
	if len(eps) != 2 {
		t.Fatalf("ListEndpointsByRuntime(rt1): got %d, want 2", len(eps))
	}

	eps2, err := s.ListEndpointsByRuntime(ctx, rt2.ID)
	if err != nil {
		t.Fatalf("ListEndpointsByRuntime(rt2): %v", err)
	}
	if len(eps2) != 1 {
		t.Fatalf("ListEndpointsByRuntime(rt2): got %d, want 1", len(eps2))
	}
}

func TestDeleteEndpointsByRuntime(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	rt1 := createTestRuntime(t, s, "runtime-1")
	rt2 := createTestRuntime(t, s, "runtime-2")
	createTestEndpoint(t, s, rt1.ID, "ep-a")
	createTestEndpoint(t, s, rt1.ID, "ep-b")
	createTestEndpoint(t, s, rt2.ID, "ep-c")

	if err := s.DeleteEndpointsByRuntime(ctx, rt1.ID); err != nil {
		t.Fatalf("DeleteEndpointsByRuntime: %v", err)
	}

	eps, _ := s.ListEndpointsByRuntime(ctx, rt1.ID)
	if len(eps) != 0 {
		t.Errorf("expected 0 endpoints for rt1 after delete, got %d", len(eps))
	}

	// rt2 endpoints should be unaffected
	eps2, _ := s.ListEndpointsByRuntime(ctx, rt2.ID)
	if len(eps2) != 1 {
		t.Errorf("expected 1 endpoint for rt2 after delete, got %d", len(eps2))
	}
}

func TestCreateAndGetSession(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	user := createTestUser(t, s, "alice", "user")
	rt := createTestRuntime(t, s, "runtime-1")
	ep := createTestEndpoint(t, s, rt.ID, "ep-1")

	sess := &Session{
		ID:         uuid.New().String(),
		OrgID:      "default",
		UserID:     user.ID,
		EndpointID: ep.ID,
		RuntimeID:  rt.ID,
		Profile:    "chat-v1",
		State:      "active",
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}

	if err := s.CreateSession(ctx, sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	got, err := s.GetSession(ctx, sess.ID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got == nil {
		t.Fatal("GetSession returned nil")
	}
	if got.UserID != user.ID {
		t.Errorf("UserID: got %q, want %q", got.UserID, user.ID)
	}
	if got.EndpointID != ep.ID {
		t.Errorf("EndpointID: got %q, want %q", got.EndpointID, ep.ID)
	}
	if got.State != "active" {
		t.Errorf("State: got %q, want %q", got.State, "active")
	}
}

func TestListSessionsByUser(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	user1 := createTestUser(t, s, "alice", "user")
	user2 := createTestUser(t, s, "bob", "user")
	rt := createTestRuntime(t, s, "runtime-1")
	ep := createTestEndpoint(t, s, rt.ID, "ep-1")

	createTestSession(t, s, user1.ID, ep.ID, rt.ID, "active")
	createTestSession(t, s, user1.ID, ep.ID, rt.ID, "active")
	createTestSession(t, s, user2.ID, ep.ID, rt.ID, "active")

	sessions, err := s.ListSessionsByUser(ctx, user1.ID)
	if err != nil {
		t.Fatalf("ListSessionsByUser: %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("ListSessionsByUser(alice): got %d, want 2", len(sessions))
	}

	sessions2, err := s.ListSessionsByUser(ctx, user2.ID)
	if err != nil {
		t.Fatalf("ListSessionsByUser(bob): %v", err)
	}
	if len(sessions2) != 1 {
		t.Fatalf("ListSessionsByUser(bob): got %d, want 1", len(sessions2))
	}
}

func TestUpdateSessionState(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	user := createTestUser(t, s, "alice", "user")
	rt := createTestRuntime(t, s, "runtime-1")
	ep := createTestEndpoint(t, s, rt.ID, "ep-1")
	sess := createTestSession(t, s, user.ID, ep.ID, rt.ID, "active")

	// Transition to idle
	if err := s.UpdateSessionState(ctx, sess.ID, "idle"); err != nil {
		t.Fatalf("UpdateSessionState(idle): %v", err)
	}
	got, _ := s.GetSession(ctx, sess.ID)
	if got.State != "idle" {
		t.Errorf("State: got %q, want %q", got.State, "idle")
	}

	// Transition to closed
	if err := s.UpdateSessionState(ctx, sess.ID, "closed"); err != nil {
		t.Fatalf("UpdateSessionState(closed): %v", err)
	}
	got, _ = s.GetSession(ctx, sess.ID)
	if got.State != "closed" {
		t.Errorf("State: got %q, want %q", got.State, "closed")
	}
}

func TestSetSessionNativeHandle(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	user := createTestUser(t, s, "alice", "user")
	rt := createTestRuntime(t, s, "runtime-1")
	ep := createTestEndpoint(t, s, rt.ID, "ep-1")
	sess := createTestSession(t, s, user.ID, ep.ID, rt.ID, "active")

	if err := s.SetSessionNativeHandle(ctx, sess.ID, "pid-12345"); err != nil {
		t.Fatalf("SetSessionNativeHandle: %v", err)
	}

	got, _ := s.GetSession(ctx, sess.ID)
	if got.NativeHandle != "pid-12345" {
		t.Errorf("NativeHandle: got %q, want %q", got.NativeHandle, "pid-12345")
	}
}

func TestListActiveSessions(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	user := createTestUser(t, s, "alice", "user")
	rt := createTestRuntime(t, s, "runtime-1")
	ep := createTestEndpoint(t, s, rt.ID, "ep-1")

	sess1 := createTestSession(t, s, user.ID, ep.ID, rt.ID, "active")
	createTestSession(t, s, user.ID, ep.ID, rt.ID, "idle")
	sess3 := createTestSession(t, s, user.ID, ep.ID, rt.ID, "active")

	// Close sess1
	if err := s.UpdateSessionState(ctx, sess1.ID, "closed"); err != nil {
		t.Fatalf("UpdateSessionState: %v", err)
	}

	active, err := s.ListActiveSessions(ctx, "default")
	if err != nil {
		t.Fatalf("ListActiveSessions: %v", err)
	}
	// sess1 is closed, sess2 is idle (not closed), sess3 is active
	if len(active) != 2 {
		t.Fatalf("ListActiveSessions: got %d, want 2", len(active))
	}

	// Verify none are sess1
	for _, a := range active {
		if a.ID == sess1.ID {
			t.Error("ListActiveSessions should not include closed session")
		}
	}
	_ = sess3 // used during creation
}

func TestCountActiveSessionsByUser(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	user := createTestUser(t, s, "alice", "user")
	rt := createTestRuntime(t, s, "runtime-1")
	ep := createTestEndpoint(t, s, rt.ID, "ep-1")

	createTestSession(t, s, user.ID, ep.ID, rt.ID, "active")
	sess2 := createTestSession(t, s, user.ID, ep.ID, rt.ID, "active")
	createTestSession(t, s, user.ID, ep.ID, rt.ID, "idle")

	count, err := s.CountActiveSessionsByUser(ctx, user.ID)
	if err != nil {
		t.Fatalf("CountActiveSessionsByUser: %v", err)
	}
	if count != 3 {
		t.Fatalf("CountActiveSessionsByUser: got %d, want 3", count)
	}

	// Close one
	s.UpdateSessionState(ctx, sess2.ID, "closed")
	count, _ = s.CountActiveSessionsByUser(ctx, user.ID)
	if count != 2 {
		t.Fatalf("CountActiveSessionsByUser after close: got %d, want 2", count)
	}
}

func TestAppendAndGetMessages(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	user := createTestUser(t, s, "alice", "user")
	rt := createTestRuntime(t, s, "runtime-1")
	ep := createTestEndpoint(t, s, rt.ID, "ep-1")
	sess := createTestSession(t, s, user.ID, ep.ID, rt.ID, "active")

	msgs := []Message{
		{ID: uuid.New().String(), SessionID: sess.ID, Seq: 1, Direction: "user", Channel: "stdin", Content: "hello", CreatedAt: time.Now()},
		{ID: uuid.New().String(), SessionID: sess.ID, Seq: 2, Direction: "agent", Channel: "stdout", Content: "hi there", CreatedAt: time.Now()},
		{ID: uuid.New().String(), SessionID: sess.ID, Seq: 3, Direction: "user", Channel: "stdin", Content: "bye", CreatedAt: time.Now()},
	}

	for i := range msgs {
		if _, err := s.AppendMessage(ctx, &msgs[i]); err != nil {
			t.Fatalf("AppendMessage[%d]: %v", i, err)
		}
	}

	// Get all messages (afterSeq=0)
	all, err := s.GetMessages(ctx, sess.ID, 0, 100)
	if err != nil {
		t.Fatalf("GetMessages: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("GetMessages(afterSeq=0): got %d, want 3", len(all))
	}

	// Get messages after seq 1
	after1, err := s.GetMessages(ctx, sess.ID, 1, 100)
	if err != nil {
		t.Fatalf("GetMessages(afterSeq=1): %v", err)
	}
	if len(after1) != 2 {
		t.Fatalf("GetMessages(afterSeq=1): got %d, want 2", len(after1))
	}
	if after1[0].Seq != 2 {
		t.Errorf("first message after seq 1: got seq %d, want 2", after1[0].Seq)
	}

	// Get messages with limit
	limited, err := s.GetMessages(ctx, sess.ID, 0, 1)
	if err != nil {
		t.Fatalf("GetMessages(limit=1): %v", err)
	}
	if len(limited) != 1 {
		t.Fatalf("GetMessages(limit=1): got %d, want 1", len(limited))
	}
}

func TestAtomicSeqAssignment(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	user := createTestUser(t, s, "alice", "user")
	rt := createTestRuntime(t, s, "runtime-1")
	ep := createTestEndpoint(t, s, rt.ID, "ep-1")
	sess := createTestSession(t, s, user.ID, ep.ID, rt.ID, "active")

	// First message should get seq 1
	msg := &Message{ID: uuid.New().String(), SessionID: sess.ID, Seq: 0, Direction: "user", Channel: "stdin", Content: "hello", CreatedAt: time.Now()}
	seq, err := s.AppendMessage(ctx, msg)
	if err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	if seq != 1 {
		t.Fatalf("first message seq: got %d, want 1", seq)
	}

	// Second message should get seq 2
	msg2 := &Message{ID: uuid.New().String(), SessionID: sess.ID, Seq: 0, Direction: "agent", Channel: "stdout", Content: "reply", CreatedAt: time.Now()}
	seq2, err := s.AppendMessage(ctx, msg2)
	if err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	if seq2 != 2 {
		t.Fatalf("second message seq: got %d, want 2", seq2)
	}

	// Third message should get seq 3
	msg3 := &Message{ID: uuid.New().String(), SessionID: sess.ID, Seq: 0, Direction: "user", Channel: "stdin", Content: "bye", CreatedAt: time.Now()}
	seq3, err := s.AppendMessage(ctx, msg3)
	if err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	if seq3 != 3 {
		t.Fatalf("third message seq: got %d, want 3", seq3)
	}

	// Verify messages are stored correctly
	all, _ := s.GetMessages(ctx, sess.ID, 0, 100)
	if len(all) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(all))
	}
}

func TestMessageExists(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	user := createTestUser(t, s, "alice", "user")
	rt := createTestRuntime(t, s, "runtime-1")
	ep := createTestEndpoint(t, s, rt.ID, "ep-1")
	sess := createTestSession(t, s, user.ID, ep.ID, rt.ID, "active")

	msgID := uuid.New().String()
	msg := &Message{ID: msgID, SessionID: sess.ID, Seq: 0, Direction: "user", Channel: "stdin", Content: "hello", CreatedAt: time.Now()}
	if _, err := s.AppendMessage(ctx, msg); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}

	exists, err := s.MessageExists(ctx, sess.ID, msgID)
	if err != nil {
		t.Fatalf("MessageExists: %v", err)
	}
	if !exists {
		t.Error("expected message to exist")
	}

	exists, err = s.MessageExists(ctx, sess.ID, "nonexistent-id")
	if err != nil {
		t.Fatalf("MessageExists (nonexistent): %v", err)
	}
	if exists {
		t.Error("expected nonexistent message to not exist")
	}
}

func TestEndpointPermissions(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	userID := uuid.New().String()
	epID1 := "ep-1"
	epID2 := "ep-2"

	// Grant access
	if err := s.GrantEndpointAccess(ctx, userID, epID1); err != nil {
		t.Fatalf("GrantEndpointAccess: %v", err)
	}
	if err := s.GrantEndpointAccess(ctx, userID, epID2); err != nil {
		t.Fatalf("GrantEndpointAccess: %v", err)
	}

	// Check access
	has, err := s.HasEndpointAccess(ctx, userID, epID1)
	if err != nil {
		t.Fatalf("HasEndpointAccess: %v", err)
	}
	if !has {
		t.Error("expected access to ep-1")
	}

	// List endpoints
	eps, err := s.ListUserEndpoints(ctx, userID)
	if err != nil {
		t.Fatalf("ListUserEndpoints: %v", err)
	}
	if len(eps) != 2 {
		t.Fatalf("ListUserEndpoints: got %d, want 2", len(eps))
	}

	// Revoke access
	if err := s.RevokeEndpointAccess(ctx, userID, epID1); err != nil {
		t.Fatalf("RevokeEndpointAccess: %v", err)
	}

	has, _ = s.HasEndpointAccess(ctx, userID, epID1)
	if has {
		t.Error("expected no access to ep-1 after revoke")
	}

	eps, _ = s.ListUserEndpoints(ctx, userID)
	if len(eps) != 1 {
		t.Fatalf("ListUserEndpoints after revoke: got %d, want 1", len(eps))
	}

	// Grant duplicate should not error (ON CONFLICT DO NOTHING)
	if err := s.GrantEndpointAccess(ctx, userID, epID2); err != nil {
		t.Fatalf("GrantEndpointAccess (duplicate): %v", err)
	}
	eps, _ = s.ListUserEndpoints(ctx, userID)
	if len(eps) != 1 {
		t.Fatalf("ListUserEndpoints after dup grant: got %d, want 1", len(eps))
	}
}

func TestAuditEvents(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	events := []*AuditEvent{
		{ID: uuid.New().String(), OrgID: "default", Action: "login.success", UserID: "u1", Detail: json.RawMessage(`{"msg":"logged in"}`), CreatedAt: time.Now()},
		{ID: uuid.New().String(), OrgID: "default", Action: "session.create", UserID: "u1", SessionID: "s1", CreatedAt: time.Now()},
		{ID: uuid.New().String(), OrgID: "default", Action: "message.sent", UserID: "u1", SessionID: "s1", Detail: json.RawMessage(`{"msg":"sent message"}`), CreatedAt: time.Now()},
	}

	for _, e := range events {
		if err := s.LogAuditEvent(ctx, e); err != nil {
			t.Fatalf("LogAuditEvent: %v", err)
		}
	}

	// List all
	all, err := s.ListAuditEvents(ctx, "default", 100, 0)
	if err != nil {
		t.Fatalf("ListAuditEvents: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("ListAuditEvents: got %d, want 3", len(all))
	}

	// List with limit
	limited, err := s.ListAuditEvents(ctx, "default", 2, 0)
	if err != nil {
		t.Fatalf("ListAuditEvents(limit=2): %v", err)
	}
	if len(limited) != 2 {
		t.Fatalf("ListAuditEvents(limit=2): got %d, want 2", len(limited))
	}

	// List with offset
	offset, err := s.ListAuditEvents(ctx, "default", 100, 2)
	if err != nil {
		t.Fatalf("ListAuditEvents(offset=2): %v", err)
	}
	if len(offset) != 1 {
		t.Fatalf("ListAuditEvents(offset=2): got %d, want 1", len(offset))
	}
}

func TestListAllSessions(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	user1 := createTestUser(t, s, "alice", "user")
	user2 := createTestUser(t, s, "bob", "user")
	rt := createTestRuntime(t, s, "runtime-1")
	ep := createTestEndpoint(t, s, rt.ID, "ep-1")

	createTestSession(t, s, user1.ID, ep.ID, rt.ID, "active")
	createTestSession(t, s, user2.ID, ep.ID, rt.ID, "active")
	createTestSession(t, s, user1.ID, ep.ID, rt.ID, "closed")

	all, err := s.ListAllSessions(ctx, "default")
	if err != nil {
		t.Fatalf("ListAllSessions: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("ListAllSessions: got %d, want 3", len(all))
	}
}

func TestPing(t *testing.T) {
	s := newTestStore(t)
	if err := s.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
}
