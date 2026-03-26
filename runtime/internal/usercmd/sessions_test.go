package usercmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amurg-ai/amurg/pkg/hubapi"
)

func TestSessionsListJSONFiltersToResumableClaudeSessions(t *testing.T) {
	t.Parallel()

	var authHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/sessions" {
			http.NotFound(w, r)
			return
		}
		authHeader = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode([]hubapi.Session{
			{ID: "sess-1", Profile: "claude-code", NativeHandle: "claude-1", MessageCount: 4},
			{ID: "sess-2", Profile: "claude-code", NativeHandle: "", MessageCount: 2},
			{ID: "sess-3", Profile: "codex", NativeHandle: "thread-1", MessageCount: 9},
		})
	}))
	defer srv.Close()

	root := NewRootCmd("test")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs([]string{
		"sessions", "list",
		"--hub-url", srv.URL,
		"--token", "jwt-token",
		"--json",
	})

	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v; stderr=%s", err, stderr.String())
	}

	if authHeader != "Bearer jwt-token" {
		t.Fatalf("Authorization header = %q, want %q", authHeader, "Bearer jwt-token")
	}

	var sessions []hubapi.Session
	if err := json.Unmarshal(stdout.Bytes(), &sessions); err != nil {
		t.Fatalf("decode JSON output: %v\noutput=%s", err, stdout.String())
	}
	if len(sessions) != 1 {
		t.Fatalf("got %d sessions, want 1", len(sessions))
	}
	if sessions[0].NativeHandle != "claude-1" {
		t.Fatalf("native_handle = %q, want %q", sessions[0].NativeHandle, "claude-1")
	}
}

func TestSessionsListHumanOutputUsesBuiltinLogin(t *testing.T) {
	t.Parallel()

	var loginCalled bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/auth/login":
			loginCalled = true
			_ = json.NewEncoder(w).Encode(map[string]string{"token": "jwt-token"})
		case "/api/sessions":
			if got := r.Header.Get("Authorization"); got != "Bearer jwt-token" {
				t.Fatalf("Authorization header = %q, want %q", got, "Bearer jwt-token")
			}
			_ = json.NewEncoder(w).Encode([]hubapi.Session{
				{ID: "sess-1", Profile: "claude-code", NativeHandle: "claude-1", MessageCount: 4},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	root := NewRootCmd("test")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs([]string{
		"sessions", "list",
		"--hub-url", srv.URL,
		"--username", "alice",
		"--password", "secret",
	})

	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v; stderr=%s", err, stderr.String())
	}
	if !loginCalled {
		t.Fatal("expected builtin login to be called")
	}

	out := stdout.String()
	if !strings.Contains(out, "NATIVE_HANDLE") {
		t.Fatalf("expected table header in output, got %q", out)
	}
	if !strings.Contains(out, "claude-1") {
		t.Fatalf("expected native handle in output, got %q", out)
	}
	if !strings.Contains(out, "4") {
		t.Fatalf("expected message count in output, got %q", out)
	}
}

func TestResolveHubBaseURLFromRuntimeConfig(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(configPath, []byte(`{"hub":{"url":"wss://hub.example.com/ws/runtime"}}`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	got, err := resolveHubBaseURL("", configPath)
	if err != nil {
		t.Fatalf("resolveHubBaseURL: %v", err)
	}
	if got != "https://hub.example.com" {
		t.Fatalf("resolveHubBaseURL = %q, want %q", got, "https://hub.example.com")
	}
}
