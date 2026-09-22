package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/amurg-ai/amurg/hub/store"
	"github.com/amurg-ai/amurg/pkg/protocol"
	"github.com/gorilla/websocket"
)

func TestRuntimeLaunchOwnershipAndRouting(t *testing.T) {
	srv, auth, db := setupTestServer(t)
	createTestUserAndGetToken(t, auth, db)
	ctx := context.Background()
	user, _ := db.GetUser(ctx, "default", "testuser")
	token := "runtime-launch-secret"
	if err := db.CreateRuntimeToken(ctx, &store.RuntimeToken{ID: "token", OrgID: "default", RuntimeID: "rt-launch", TokenHash: sha256hex(token), CreatedBy: user.ID, CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(srv.Handler())
	defer server.Close()
	ws, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/ws/runtime", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	err = ws.WriteJSON(protocol.Envelope{Type: protocol.TypeRuntimeHello, Payload: protocol.RuntimeHello{RuntimeID: "rt-launch", Token: token, LocalLaunch: true, Agents: []protocol.AgentRegistration{{ID: "codex-launch", Profile: "codex", Caps: protocol.KnownProfiles["codex"]}}}})
	if err != nil {
		t.Fatal(err)
	}
	var ack protocol.Envelope
	if err := ws.ReadJSON(&ack); err != nil {
		t.Fatal(err)
	}
	request := func(method, path, token, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, req)
		return w
	}
	body := `{"agent_id":"codex-launch","user_id":"attacker","launch":{"work_dir":"/tmp","skip_sandbox":true}}`
	if w := request("POST", "/api/runtime/sessions", "wrong", body); w.Code != 401 {
		t.Fatalf("bad token: %d", w.Code)
	}
	w := request("POST", "/api/runtime/sessions", token, body)
	if w.Code != 201 {
		t.Fatalf("launch %d: %s", w.Code, w.Body)
	}
	var sess store.Session
	if err := json.Unmarshal(w.Body.Bytes(), &sess); err != nil {
		t.Fatal(err)
	}
	if sess.UserID != user.ID || sess.RuntimeID != "rt-launch" {
		t.Fatalf("wrong owner: %+v", sess)
	}
	_ = ws.SetReadDeadline(time.Now().Add(3 * time.Second))
	var env protocol.Envelope
	if err := ws.ReadJSON(&env); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(env.Payload)
	var launch protocol.SessionCreate
	_ = json.Unmarshal(raw, &launch)
	if env.Type != protocol.TypeSessionCreate || launch.Launch == nil || launch.Launch.WorkDir != "/tmp" || !launch.Launch.SkipSandbox {
		t.Fatalf("launch not delivered: %+v", env)
	}
	if w := request("GET", "/api/runtime/sessions/"+sess.ID, token, ""); w.Code != http.StatusOK {
		t.Fatalf("read own: %d", w.Code)
	}
	if err := db.CreateRuntimeToken(ctx, &store.RuntimeToken{ID: "other-token", OrgID: "default", RuntimeID: "other-runtime", TokenHash: sha256hex("other"), CreatedBy: user.ID, CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if w := request("POST", "/api/runtime/sessions", "other", body); w.Code != 403 {
		t.Fatalf("cross runtime create: %d", w.Code)
	}
	if w := request("GET", "/api/runtime/sessions/"+sess.ID, "other", ""); w.Code != 404 {
		t.Fatalf("cross runtime read: %d", w.Code)
	}
	if err := db.RevokeRuntimeToken(ctx, "token"); err != nil {
		t.Fatal(err)
	}
	if w := request("POST", "/api/runtime/sessions", token, body); w.Code != 401 {
		t.Fatalf("revoked token: %d", w.Code)
	}
}
