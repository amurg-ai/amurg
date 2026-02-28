// Package config handles runtime configuration loading and validation.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// Config is the top-level runtime configuration.
type Config struct {
	Hub       HubConfig       `json:"hub"`
	Runtime   RuntimeConfig   `json:"runtime"`
	Agents []AgentConfig `json:"agents"`
}

// HubConfig defines how the runtime connects to the hub.
type HubConfig struct {
	URL               string        `json:"url"`
	Token             string        `json:"token"`
	TLSSkipVerify     bool          `json:"tls_skip_verify,omitempty"` // dev only
	ReconnectInterval Duration      `json:"reconnect_interval,omitempty"`
	MaxReconnectDelay Duration      `json:"max_reconnect_delay,omitempty"`
}

// RuntimeConfig defines global runtime limits.
type RuntimeConfig struct {
	ID              string   `json:"id"`
	OrgID           string   `json:"org_id,omitempty"` // optional, defaults to "default"
	MaxSessions     int      `json:"max_sessions"`
	DefaultTimeout  Duration `json:"default_timeout"`
	MaxOutputBytes  int64    `json:"max_output_bytes"`
	IdleTimeout     Duration `json:"idle_timeout"`
	LogLevel        string   `json:"log_level"`
	FileStoragePath         string   `json:"file_storage_path,omitempty"`         // path for file storage; default "./amurg-files"
	MaxFileBytes            int64    `json:"max_file_bytes,omitempty"`            // max file size; default 10MB
	AllowRemotePermissionSkip bool  `json:"allow_remote_permission_skip,omitempty"`
}

// SecurityConfig defines security constraints for an agent.
type SecurityConfig struct {
	AllowedPaths   []string `json:"allowed_paths,omitempty"`
	DeniedPaths    []string `json:"denied_paths,omitempty"`
	AllowedTools   []string `json:"allowed_tools,omitempty"`
	PermissionMode string   `json:"permission_mode,omitempty"`
	Cwd            string   `json:"cwd,omitempty"`
	EnvWhitelist   []string `json:"env_whitelist,omitempty"`
}

// AgentConfig defines a single agent configuration.
type AgentConfig struct {
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	Profile  string            `json:"profile"`
	Tags     map[string]string `json:"tags,omitempty"`
	Limits   *AgentLimits      `json:"limits,omitempty"`
	Security *SecurityConfig   `json:"security,omitempty"`

	// Profile-specific settings (parsed by the adapter)
	CLI        *CLIConfig        `json:"cli,omitempty"`
	ClaudeCode *ClaudeCodeConfig `json:"claude_code,omitempty"`
	Copilot    *CopilotConfig    `json:"copilot,omitempty"`
	Codex      *CodexConfig      `json:"codex,omitempty"`
	Kilo       *KiloConfig       `json:"kilo,omitempty"`
	Gemini     *GeminiCLIConfig  `json:"gemini,omitempty"`
	Job        *JobConfig        `json:"job,omitempty"`
	HTTP       *HTTPConfig       `json:"http,omitempty"`
	External   *ExternalConfig   `json:"external,omitempty"`
}

// WorkDir returns the working directory configured for this agent, if any.
func (a AgentConfig) WorkDir() string {
	switch {
	case a.ClaudeCode != nil && a.ClaudeCode.WorkDir != "":
		return a.ClaudeCode.WorkDir
	case a.CLI != nil && a.CLI.WorkDir != "":
		return a.CLI.WorkDir
	case a.Copilot != nil && a.Copilot.WorkDir != "":
		return a.Copilot.WorkDir
	case a.Codex != nil && a.Codex.WorkDir != "":
		return a.Codex.WorkDir
	case a.Kilo != nil && a.Kilo.WorkDir != "":
		return a.Kilo.WorkDir
	case a.Gemini != nil && a.Gemini.WorkDir != "":
		return a.Gemini.WorkDir
	case a.Job != nil && a.Job.WorkDir != "":
		return a.Job.WorkDir
	case a.External != nil && a.External.WorkDir != "":
		return a.External.WorkDir
	default:
		return ""
	}
}

// AgentLimits are per-agent operational limits.
type AgentLimits struct {
	MaxSessions    int      `json:"max_sessions,omitempty"`
	SessionTimeout Duration `json:"session_timeout,omitempty"`
	MaxOutputBytes int64    `json:"max_output_bytes,omitempty"`
	IdleTimeout    Duration `json:"idle_timeout,omitempty"`
}

// CLIConfig is config for generic-cli and github-copilot profiles.
type CLIConfig struct {
	Command    string            `json:"command"`
	Args       []string          `json:"args,omitempty"`
	WorkDir    string            `json:"work_dir,omitempty"`
	Env        map[string]string `json:"env,omitempty"`
	SpawnPolicy string           `json:"spawn_policy,omitempty"` // "per-session" (default) or "persistent"
}

// ClaudeCodeConfig is config for the claude-code profile.
type ClaudeCodeConfig struct {
	Command        string            `json:"command,omitempty"`         // default: "claude"
	WorkDir        string            `json:"work_dir,omitempty"`
	Env            map[string]string `json:"env,omitempty"`
	Model          string            `json:"model,omitempty"`           // e.g. "sonnet"
	PermissionMode string            `json:"permission_mode,omitempty"` // e.g. "dangerously-skip-permissions"
	MaxTurns       int               `json:"max_turns,omitempty"`
	AllowedTools   []string          `json:"allowed_tools,omitempty"`
	SystemPrompt   string            `json:"system_prompt,omitempty"`
}

// CopilotConfig is config for the github-copilot profile.
type CopilotConfig struct {
	Command                string            `json:"command,omitempty"`                  // default: "copilot"
	WorkDir                string            `json:"work_dir,omitempty"`
	Env                    map[string]string `json:"env,omitempty"`
	Model                  string            `json:"model,omitempty"`                    // e.g. "claude-sonnet-4.5"
	AllowedTools           []string          `json:"allowed_tools,omitempty"`            // --allow-tool glob patterns
	DeniedTools            []string          `json:"denied_tools,omitempty"`             // --deny-tool glob patterns
	MaxAutopilotContinues  int               `json:"max_autopilot_continues,omitempty"`  // --max-autopilot-continues N (implies --autopilot)
}

// CodexConfig is config for the codex profile.
type CodexConfig struct {
	Command        string            `json:"command,omitempty"`         // default: "codex"
	WorkDir        string            `json:"work_dir,omitempty"`
	Env            map[string]string `json:"env,omitempty"`
	Model          string            `json:"model,omitempty"`           // e.g. "gpt-5.3-codex"
	ApprovalMode   string            `json:"approval_mode,omitempty"`   // "untrusted", "on-request", "never"
	SandboxMode    string            `json:"sandbox_mode,omitempty"`    // "read-only", "workspace-write", "danger-full-access"
	Profile        string            `json:"profile,omitempty"`         // named config profile (-p flag)
	AdditionalDirs []string          `json:"additional_dirs,omitempty"` // --add-dir paths for extra write access
	FullAuto       bool              `json:"full_auto,omitempty"`       // --full-auto convenience preset
}

// KiloConfig is config for the kilo-code profile.
type KiloConfig struct {
	Command      string            `json:"command,omitempty"`       // default: "kilo"
	WorkDir      string            `json:"work_dir,omitempty"`
	Env          map[string]string `json:"env,omitempty"`
	Model        string            `json:"model,omitempty"`         // e.g. "anthropic/claude-sonnet-4"
	Provider     string            `json:"provider,omitempty"`      // e.g. "anthropic", "openrouter"
	Mode         string            `json:"mode,omitempty"`          // "code", "architect", "debugger", "ask", "orchestrator"
	SystemPrompt string            `json:"system_prompt,omitempty"` // --append-system-prompt
	Timeout      int               `json:"timeout,omitempty"`       // --timeout seconds (requires --auto)
}

// GeminiCLIConfig is config for the gemini-cli profile.
type GeminiCLIConfig struct {
	Command          string            `json:"command,omitempty"`            // default: "gemini"
	WorkDir          string            `json:"work_dir,omitempty"`
	Env              map[string]string `json:"env,omitempty"`
	Model            string            `json:"model,omitempty"`              // e.g. "gemini-2.5-pro", "gemini-2.5-flash"
	ApprovalMode     string            `json:"approval_mode,omitempty"`      // "default", "auto_edit", "yolo"
	SystemPromptFile string            `json:"system_prompt_file,omitempty"` // path to GEMINI.md override
	IncludeDirs      []string          `json:"include_directories,omitempty"`
	Sandbox          bool              `json:"sandbox,omitempty"`
}

// JobConfig is config for generic-job and codex profiles.
type JobConfig struct {
	Command    string            `json:"command"`
	Args       []string          `json:"args,omitempty"`
	WorkDir    string            `json:"work_dir,omitempty"`
	Env        map[string]string `json:"env,omitempty"`
	MaxRuntime Duration          `json:"max_runtime,omitempty"`
}

// HTTPConfig is config for generic-http profile.
type HTTPConfig struct {
	BaseURL string            `json:"base_url"`
	Method  string            `json:"method,omitempty"` // default POST
	Headers map[string]string `json:"headers,omitempty"`
	Timeout Duration          `json:"timeout,omitempty"`
}

// ExternalConfig is config for the external profile (JSON-Lines stdio adapter).
type ExternalConfig struct {
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	WorkDir string            `json:"work_dir,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

// Duration is a JSON-friendly time.Duration (accepts strings like "30s", "5m").
type Duration struct {
	time.Duration
}

func (d *Duration) UnmarshalJSON(b []byte) error {
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	switch val := v.(type) {
	case string:
		dur, err := time.ParseDuration(val)
		if err != nil {
			return err
		}
		d.Duration = dur
	case float64:
		d.Duration = time.Duration(val) * time.Second
	default:
		return fmt.Errorf("invalid duration: %v", v)
	}
	return nil
}

func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(d.String())
}

// Load reads and validates a config file.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("validate config: %w", err)
	}

	cfg.applyDefaults()
	return &cfg, nil
}

func (c *Config) validate() error {
	if c.Hub.URL == "" {
		return fmt.Errorf("hub.url is required")
	}
	if c.Hub.Token == "" {
		return fmt.Errorf("hub.token is required")
	}
	if c.Runtime.ID == "" {
		return fmt.Errorf("runtime.id is required")
	}
	if len(c.Agents) == 0 {
		return fmt.Errorf("at least one agent is required")
	}
	seen := make(map[string]bool)
	for i, agent := range c.Agents {
		if agent.ID == "" {
			return fmt.Errorf("agents[%d].id is required", i)
		}
		if seen[agent.ID] {
			return fmt.Errorf("duplicate agent id: %s", agent.ID)
		}
		seen[agent.ID] = true
		if agent.Profile == "" {
			return fmt.Errorf("agents[%d].profile is required", i)
		}
		if agent.Security != nil && agent.Security.PermissionMode != "" {
			switch agent.Security.PermissionMode {
			case "skip", "strict", "auto":
				// valid
			default:
				return fmt.Errorf("agents[%d].security.permission_mode must be skip, strict, or auto", i)
			}
		}
	}
	return nil
}

func (c *Config) applyDefaults() {
	if c.Runtime.MaxSessions == 0 {
		c.Runtime.MaxSessions = 10
	}
	if c.Runtime.DefaultTimeout.Duration == 0 {
		c.Runtime.DefaultTimeout.Duration = 30 * time.Minute
	}
	if c.Runtime.MaxOutputBytes == 0 {
		c.Runtime.MaxOutputBytes = 10 * 1024 * 1024 // 10MB
	}
	if c.Runtime.IdleTimeout.Duration == 0 {
		c.Runtime.IdleTimeout.Duration = 30 * time.Second
	}
	if c.Runtime.LogLevel == "" {
		c.Runtime.LogLevel = "info"
	}
	if c.Hub.ReconnectInterval.Duration == 0 {
		c.Hub.ReconnectInterval.Duration = 2 * time.Second
	}
	if c.Hub.MaxReconnectDelay.Duration == 0 {
		c.Hub.MaxReconnectDelay.Duration = 60 * time.Second
	}
	if c.Runtime.FileStoragePath == "" {
		c.Runtime.FileStoragePath = "./amurg-files"
	}
	if c.Runtime.MaxFileBytes == 0 {
		c.Runtime.MaxFileBytes = 10 * 1024 * 1024 // 10MB
	}
}
