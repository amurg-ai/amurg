package hubapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNormalizeBaseURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "wss runtime", in: "wss://hub.example.com/ws/runtime", want: "https://hub.example.com"},
		{name: "ws client", in: "ws://localhost:8080/ws/client", want: "http://localhost:8080"},
		{name: "https base", in: "https://hub.example.com/amurg/", want: "https://hub.example.com/amurg"},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := NormalizeBaseURL(tc.in)
			if err != nil {
				t.Fatalf("NormalizeBaseURL(%q): %v", tc.in, err)
			}
			if got != tc.want {
				t.Fatalf("NormalizeBaseURL(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestClientLoginAndListSessions(t *testing.T) {
	t.Parallel()

	var authHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/auth/login":
			if r.Method != http.MethodPost {
				t.Fatalf("unexpected method %s", r.Method)
			}
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode login body: %v", err)
			}
			if body["username"] != "alice" || body["password"] != "secret" {
				t.Fatalf("unexpected login payload: %#v", body)
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"token": "jwt-token"})
		case "/api/sessions":
			authHeader = r.Header.Get("Authorization")
			_ = json.NewEncoder(w).Encode([]Session{{
				ID:           "sess-1",
				Profile:      "claude-code",
				NativeHandle: "claude-session-123",
				MessageCount: 7,
			}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client, err := New(srv.URL, srv.Client())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	token, err := client.Login(context.Background(), "alice", "secret")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if token != "jwt-token" {
		t.Fatalf("Login token = %q, want %q", token, "jwt-token")
	}

	client.SetToken(token)
	sessions, err := client.ListSessions(context.Background())
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}

	if authHeader != "Bearer jwt-token" {
		t.Fatalf("Authorization header = %q, want %q", authHeader, "Bearer jwt-token")
	}
	if len(sessions) != 1 {
		t.Fatalf("got %d sessions, want 1", len(sessions))
	}
	if sessions[0].NativeHandle != "claude-session-123" {
		t.Fatalf("native_handle = %q, want %q", sessions[0].NativeHandle, "claude-session-123")
	}
	if sessions[0].MessageCount != 7 {
		t.Fatalf("message_count = %d, want 7", sessions[0].MessageCount)
	}
}
