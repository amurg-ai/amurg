package usercmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/amurg-ai/amurg/pkg/protocol"
)

func TestStartUsesRuntimeRegistrationAndForwardsLocalOptions(t *testing.T) {
	dir := t.TempDir()
	var requests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer runtime-secret" {
			t.Error("wrong authentication")
		}
		requests++
		switch r.Method {
		case "POST":
			if r.URL.Path != "/api/runtime/sessions" {
				t.Errorf("path %s", r.URL.Path)
			}
			var req struct {
				AgentID string                 `json:"agent_id"`
				Launch  protocol.SessionLaunch `json:"launch"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Error(err)
			}
			if req.AgentID != "codex-1" || req.Launch.WorkDir != dir || !req.Launch.SkipSandbox || len(req.Launch.Args) != 2 || req.Launch.Args[1] != "existing-session" {
				t.Errorf("request: %+v", req)
			}
			w.WriteHeader(201)
			_, _ = w.Write([]byte(`{"id":"new-session","state":"creating"}`))
		case "GET":
			if r.URL.Path != "/api/runtime/sessions/new-session" {
				t.Errorf("path %s", r.URL.Path)
			}
			_, _ = w.Write([]byte(`{"id":"new-session","state":"active"}`))
		}
	}))
	defer srv.Close()
	path := filepath.Join(t.TempDir(), "config.json")
	b, _ := json.Marshal(map[string]any{"hub": map[string]string{"url": srv.URL, "token": "runtime-secret"}, "runtime": map[string]string{"id": "rt"}, "agents": []any{map[string]string{"id": "codex-1", "profile": "codex"}}})
	if err := os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
	cmd := NewRootCmd("test")
	cmd.SetArgs([]string{"start", "codex", "--config", path, "--cwd", dir, "--skip-sandbox", "--", "resume", "existing-session"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if requests != 2 {
		t.Fatalf("expected creation and startup check, got %d requests", requests)
	}
}
