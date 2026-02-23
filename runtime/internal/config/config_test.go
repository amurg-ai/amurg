package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDuration_UnmarshalJSON_String(t *testing.T) {
	var d Duration
	err := json.Unmarshal([]byte(`"30s"`), &d)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.Duration != 30*time.Second {
		t.Errorf("expected 30s, got %v", d.Duration)
	}
}

func TestDuration_UnmarshalJSON_Minutes(t *testing.T) {
	var d Duration
	err := json.Unmarshal([]byte(`"5m"`), &d)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.Duration != 5*time.Minute {
		t.Errorf("expected 5m, got %v", d.Duration)
	}
}

func TestDuration_UnmarshalJSON_Number(t *testing.T) {
	var d Duration
	err := json.Unmarshal([]byte(`10`), &d)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.Duration != 10*time.Second {
		t.Errorf("expected 10s, got %v", d.Duration)
	}
}

func TestDuration_UnmarshalJSON_Invalid(t *testing.T) {
	var d Duration
	err := json.Unmarshal([]byte(`"not-a-duration"`), &d)
	if err == nil {
		t.Fatal("expected error for invalid duration string")
	}
}

func TestDuration_UnmarshalJSON_InvalidType(t *testing.T) {
	var d Duration
	err := json.Unmarshal([]byte(`true`), &d)
	if err == nil {
		t.Fatal("expected error for boolean duration")
	}
}

func TestDuration_MarshalJSON(t *testing.T) {
	d := Duration{Duration: 2 * time.Minute}
	data, err := d.MarshalJSON()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(data) != `"2m0s"` {
		t.Errorf("expected \"2m0s\", got %s", string(data))
	}
}

func TestDuration_RoundTrip(t *testing.T) {
	original := Duration{Duration: 45 * time.Second}
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var decoded Duration
	err = json.Unmarshal(data, &decoded)
	if err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if decoded.Duration != original.Duration {
		t.Errorf("round-trip mismatch: expected %v, got %v", original.Duration, decoded.Duration)
	}
}

func TestLoad_ValidConfig(t *testing.T) {
	cfgJSON := `{
		"hub": {
			"url": "ws://localhost:8090/ws/runtime",
			"token": "test-token"
		},
		"runtime": {
			"id": "test-runtime",
			"max_sessions": 5,
			"idle_timeout": "10s"
		},
		"endpoints": [
			{
				"id": "ep-1",
				"name": "Test",
				"profile": "generic-cli",
				"cli": {
					"command": "echo",
					"args": ["hello"]
				}
			}
		]
	}`

	path := writeTemp(t, cfgJSON)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Hub.URL != "ws://localhost:8090/ws/runtime" {
		t.Errorf("wrong hub URL: %s", cfg.Hub.URL)
	}
	if cfg.Hub.Token != "test-token" {
		t.Errorf("wrong hub token: %s", cfg.Hub.Token)
	}
	if cfg.Runtime.ID != "test-runtime" {
		t.Errorf("wrong runtime ID: %s", cfg.Runtime.ID)
	}
	if cfg.Runtime.MaxSessions != 5 {
		t.Errorf("wrong max sessions: %d", cfg.Runtime.MaxSessions)
	}
	if cfg.Runtime.IdleTimeout.Duration != 10*time.Second {
		t.Errorf("wrong idle timeout: %v", cfg.Runtime.IdleTimeout.Duration)
	}
	if len(cfg.Endpoints) != 1 {
		t.Fatalf("expected 1 endpoint, got %d", len(cfg.Endpoints))
	}
	if cfg.Endpoints[0].ID != "ep-1" {
		t.Errorf("wrong endpoint ID: %s", cfg.Endpoints[0].ID)
	}
}

func TestLoad_AppliesDefaults(t *testing.T) {
	cfgJSON := `{
		"hub": {
			"url": "ws://localhost:8090/ws/runtime",
			"token": "test-token"
		},
		"runtime": {
			"id": "test-runtime"
		},
		"endpoints": [
			{
				"id": "ep-1",
				"name": "Test",
				"profile": "generic-cli"
			}
		]
	}`

	path := writeTemp(t, cfgJSON)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Runtime.MaxSessions != 10 {
		t.Errorf("expected default max_sessions 10, got %d", cfg.Runtime.MaxSessions)
	}
	if cfg.Runtime.DefaultTimeout.Duration != 30*time.Minute {
		t.Errorf("expected default timeout 30m, got %v", cfg.Runtime.DefaultTimeout.Duration)
	}
	if cfg.Runtime.MaxOutputBytes != 10*1024*1024 {
		t.Errorf("expected default max output 10MB, got %d", cfg.Runtime.MaxOutputBytes)
	}
	if cfg.Runtime.IdleTimeout.Duration != 30*time.Second {
		t.Errorf("expected default idle timeout 30s, got %v", cfg.Runtime.IdleTimeout.Duration)
	}
	if cfg.Runtime.LogLevel != "info" {
		t.Errorf("expected default log level info, got %s", cfg.Runtime.LogLevel)
	}
	if cfg.Hub.ReconnectInterval.Duration != 2*time.Second {
		t.Errorf("expected default reconnect interval 2s, got %v", cfg.Hub.ReconnectInterval.Duration)
	}
	if cfg.Hub.MaxReconnectDelay.Duration != 60*time.Second {
		t.Errorf("expected default max reconnect delay 60s, got %v", cfg.Hub.MaxReconnectDelay.Duration)
	}
}

func TestLoad_MissingHubURL(t *testing.T) {
	cfgJSON := `{
		"hub": {"token": "test"},
		"runtime": {"id": "r1"},
		"endpoints": [{"id": "e1", "name": "n", "profile": "p"}]
	}`
	path := writeTemp(t, cfgJSON)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error for missing hub.url")
	}
}

func TestLoad_MissingHubToken(t *testing.T) {
	cfgJSON := `{
		"hub": {"url": "ws://localhost"},
		"runtime": {"id": "r1"},
		"endpoints": [{"id": "e1", "name": "n", "profile": "p"}]
	}`
	path := writeTemp(t, cfgJSON)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error for missing hub.token")
	}
}

func TestLoad_MissingRuntimeID(t *testing.T) {
	cfgJSON := `{
		"hub": {"url": "ws://localhost", "token": "t"},
		"runtime": {},
		"endpoints": [{"id": "e1", "name": "n", "profile": "p"}]
	}`
	path := writeTemp(t, cfgJSON)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error for missing runtime.id")
	}
}

func TestLoad_NoEndpoints(t *testing.T) {
	cfgJSON := `{
		"hub": {"url": "ws://localhost", "token": "t"},
		"runtime": {"id": "r1"},
		"endpoints": []
	}`
	path := writeTemp(t, cfgJSON)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error for no endpoints")
	}
}

func TestLoad_MissingEndpointID(t *testing.T) {
	cfgJSON := `{
		"hub": {"url": "ws://localhost", "token": "t"},
		"runtime": {"id": "r1"},
		"endpoints": [{"name": "n", "profile": "p"}]
	}`
	path := writeTemp(t, cfgJSON)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error for missing endpoint ID")
	}
}

func TestLoad_MissingEndpointProfile(t *testing.T) {
	cfgJSON := `{
		"hub": {"url": "ws://localhost", "token": "t"},
		"runtime": {"id": "r1"},
		"endpoints": [{"id": "e1", "name": "n"}]
	}`
	path := writeTemp(t, cfgJSON)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error for missing endpoint profile")
	}
}

func TestLoad_DuplicateEndpointID(t *testing.T) {
	cfgJSON := `{
		"hub": {"url": "ws://localhost", "token": "t"},
		"runtime": {"id": "r1"},
		"endpoints": [
			{"id": "e1", "name": "first", "profile": "p"},
			{"id": "e1", "name": "second", "profile": "p"}
		]
	}`
	path := writeTemp(t, cfgJSON)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error for duplicate endpoint ID")
	}
}

func TestLoad_InvalidJSON(t *testing.T) {
	path := writeTemp(t, "not json at all")
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestLoad_FileNotFound(t *testing.T) {
	_, err := Load("/nonexistent/path/config.json")
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
}

// writeTemp creates a temporary file with the given content and returns its path.
func writeTemp(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	return path
}
