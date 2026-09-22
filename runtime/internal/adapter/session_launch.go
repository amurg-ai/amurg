package adapter

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/amurg-ai/amurg/pkg/protocol"
	"github.com/amurg-ai/amurg/runtime/internal/config"
)

// WithSessionLaunch keeps launch options private to this session and saves them
// so lazy recreation after a runtime restart uses the same directory and args.
func WithSessionLaunch(cfg config.AgentConfig, launch *protocol.SessionLaunch) (config.AgentConfig, error) {
	if cfg.SessionKey == "" {
		return cfg, fmt.Errorf("session identity required")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return cfg, err
	}
	sum := sha256.Sum256([]byte(cfg.SessionKey))
	root := filepath.Join(home, ".amurg", "conversations", fmt.Sprintf("%x", sum[:16]))
	path := filepath.Join(root, "launch.json")
	supplied := launch != nil
	if launch == nil {
		data, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			return cfg, nil
		}
		if err != nil {
			return cfg, err
		}
		launch = &protocol.SessionLaunch{}
		if err := json.Unmarshal(data, launch); err != nil {
			return cfg, err
		}
	}
	if !filepath.IsAbs(launch.WorkDir) {
		return cfg, fmt.Errorf("working directory must be absolute")
	}
	info, err := os.Stat(launch.WorkDir)
	if err != nil || !info.IsDir() {
		return cfg, fmt.Errorf("working directory does not exist: %s", launch.WorkDir)
	}
	for _, arg := range launch.Args {
		if strings.ContainsRune(arg, 0) {
			return cfg, fmt.Errorf("invalid agent argument")
		}
	}
	if cfg.Security != nil {
		security := *cfg.Security
		security.Cwd = launch.WorkDir
		cfg.Security = &security
	}
	switch cfg.Profile {
	case protocol.ProfileCodex:
		c := config.CodexConfig{}
		if cfg.Codex != nil {
			c = *cfg.Codex
		}
		c.WorkDir = launch.WorkDir
		c.Args = append(append([]string{}, c.Args...), launch.Args...)
		if launch.Yolo || launch.SkipSandbox {
			c.Args = append(c.Args, "--dangerously-bypass-approvals-and-sandbox")
		}
		cfg.Codex = &c
	case protocol.ProfileOpenCode:
		if launch.SkipSandbox {
			return cfg, fmt.Errorf("--skip-sandbox is only supported for Codex; use --yolo for OpenCode")
		}
		c := config.OpenCodeConfig{}
		if cfg.OpenCode != nil {
			c = *cfg.OpenCode
		}
		c.WorkDir = launch.WorkDir
		c.Args = append(append([]string{}, c.Args...), launch.Args...)
		if launch.Yolo {
			c.Args = append(c.Args, "--auto")
		}
		cfg.OpenCode = &c
	default:
		return cfg, fmt.Errorf("local launch currently supports codex and opencode")
	}
	if supplied {
		data, err := json.Marshal(launch)
		if err != nil {
			return cfg, err
		}
		if err := os.MkdirAll(root, 0700); err != nil {
			return cfg, err
		}
		temp, err := os.CreateTemp(root, "launch-*.tmp")
		if err != nil {
			return cfg, err
		}
		defer func() { _ = os.Remove(temp.Name()) }()
		if _, err := temp.Write(data); err != nil {
			_ = temp.Close()
			return cfg, err
		}
		if err := temp.Close(); err != nil {
			return cfg, err
		}
		if err := os.Rename(temp.Name(), path); err != nil {
			return cfg, err
		}
	}
	return cfg, nil
}
