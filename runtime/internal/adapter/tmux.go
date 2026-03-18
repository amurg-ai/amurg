package adapter

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

var tmuxSessionNameSanitizer = regexp.MustCompile(`[^a-zA-Z0-9_-]+`)

func ensureTMuxInstalled() error {
	if _, err := exec.LookPath("tmux"); err != nil {
		return fmt.Errorf("tmux is required for this transport: %w", err)
	}
	return nil
}

func tmuxSessionName(prefix string) string {
	prefix = tmuxSessionNameSanitizer.ReplaceAllString(prefix, "-")
	prefix = strings.Trim(prefix, "-")
	if prefix == "" {
		prefix = "amurg"
	}
	return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
}

func tmuxShellQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func tmuxCommandString(args []string) string {
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		quoted = append(quoted, tmuxShellQuote(arg))
	}
	return strings.Join(quoted, " ")
}

func tmuxRun(args ...string) error {
	cmd := exec.Command("tmux", args...)
	if output, err := cmd.CombinedOutput(); err != nil {
		trimmed := strings.TrimSpace(string(output))
		if trimmed != "" {
			return fmt.Errorf("tmux %s: %s", strings.Join(args, " "), trimmed)
		}
		return fmt.Errorf("tmux %s: %w", strings.Join(args, " "), err)
	}
	return nil
}

func tmuxRunOutput(args ...string) (string, error) {
	cmd := exec.Command("tmux", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		trimmed := strings.TrimSpace(string(output))
		if trimmed != "" {
			return "", fmt.Errorf("tmux %s: %s", strings.Join(args, " "), trimmed)
		}
		return "", fmt.Errorf("tmux %s: %w", strings.Join(args, " "), err)
	}
	return string(output), nil
}

func tmuxHasSession(sessionName string) bool {
	if sessionName == "" {
		return false
	}
	return exec.Command("tmux", "has-session", "-t", sessionName).Run() == nil
}

func tmuxCreateSession(sessionName, workDir string, command []string) error {
	return tmuxRun("new-session", "-d", "-s", sessionName, "-c", workDir, tmuxCommandString(command))
}

func tmuxPipePane(target, logPath string) error {
	pipeCmd := fmt.Sprintf("cat >> %s", tmuxShellQuote(logPath))
	return tmuxRun("pipe-pane", "-o", "-t", target, pipeCmd)
}

func tmuxCapturePane(target string, lines int) (string, error) {
	start := fmt.Sprintf("-%d", lines)
	return tmuxRunOutput("capture-pane", "-p", "-e", "-t", target, "-S", start)
}

func tmuxSendLiteral(target, text string) error {
	tmp, err := os.CreateTemp("", "amurg-tmux-input-*.txt")
	if err != nil {
		return fmt.Errorf("create tmux input file: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if _, err := tmp.WriteString(text); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write tmux input file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close tmux input file: %w", err)
	}

	bufferName := tmuxSessionName("amurg-buffer")
	if err := tmuxRun("load-buffer", "-b", bufferName, tmpPath); err != nil {
		return err
	}
	defer func() { _ = tmuxRun("delete-buffer", "-b", bufferName) }()

	if err := tmuxRun("paste-buffer", "-d", "-b", bufferName, "-t", target); err != nil {
		return err
	}
	return nil
}

func tmuxSendKeys(target string, keys ...string) error {
	args := append([]string{"send-keys", "-t", target}, keys...)
	return tmuxRun(args...)
}

func tmuxKillSession(sessionName string) error {
	if !tmuxHasSession(sessionName) {
		return nil
	}
	return tmuxRun("kill-session", "-t", sessionName)
}
