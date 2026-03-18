package wizard

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"

	"github.com/amurg-ai/amurg/pkg/cli"
	"github.com/amurg-ai/amurg/runtime/internal/config"
)

var (
	lookPath      = exec.LookPath
	commandRunner = func(name string, args ...string) *exec.Cmd {
		return exec.Command(name, args...)
	}
	geteuid = os.Geteuid
)

// ConfigNeedsTMux reports whether the config includes any agents that require tmux.
func ConfigNeedsTMux(cfg *config.Config) bool {
	if cfg == nil {
		return false
	}
	for _, agent := range cfg.Agents {
		if agent.Codex != nil && agent.Codex.Transport == "tmux" {
			return true
		}
	}
	return false
}

// TMuxInstalled reports whether tmux is available on PATH.
func TMuxInstalled() bool {
	_, err := lookPath("tmux")
	return err == nil
}

// EnsureTMuxForConfig offers to install tmux when the config requires it for
// native interactive sessions and tmux is not already installed.
func EnsureTMuxForConfig(p *cli.Prompter, cfg *config.Config) error {
	if !ConfigNeedsTMux(cfg) || TMuxInstalled() {
		return nil
	}
	if p == nil {
		p = cli.DefaultPrompter()
	}

	_, _ = fmt.Fprintln(p.Out)
	_, _ = fmt.Fprintln(p.Out, "  tmux is required for interactive Codex sessions.")
	if !p.Confirm("  Install tmux now for interactive sessions?", true) {
		printTMuxManualInstructions(p.Out)
		return nil
	}

	plan, err := detectTMuxInstallPlan()
	if err != nil {
		_, _ = fmt.Fprintf(p.Out, "  Couldn't determine how to install tmux automatically: %v\n", err)
		printTMuxManualInstructions(p.Out)
		return nil
	}

	_, _ = fmt.Fprintf(p.Out, "  Installing tmux using: %s\n", plan.Display)
	cmd := commandRunner(plan.Command, plan.Args...)
	cmd.Stdin = p.In
	cmd.Stdout = p.Out
	cmd.Stderr = p.Out
	if err := cmd.Run(); err != nil {
		_, _ = fmt.Fprintf(p.Out, "  tmux installation failed: %v\n", err)
		printTMuxManualInstructions(p.Out)
		return nil
	}
	if !TMuxInstalled() {
		_, _ = fmt.Fprintln(p.Out, "  tmux install command finished, but tmux is still not on PATH.")
		printTMuxManualInstructions(p.Out)
		return nil
	}

	_, _ = fmt.Fprintln(p.Out, "  tmux installed successfully.")
	return nil
}

type tmuxInstallPlan struct {
	Command string
	Args    []string
	Display string
}

func detectTMuxInstallPlan() (tmuxInstallPlan, error) {
	base, display, err := baseTMuxInstallCommand()
	if err != nil {
		return tmuxInstallPlan{}, err
	}
	command, args, err := withPrivilege(base)
	if err != nil {
		return tmuxInstallPlan{}, err
	}
	return tmuxInstallPlan{Command: command, Args: args, Display: display}, nil
}

func baseTMuxInstallCommand() ([]string, string, error) {
	if runtime.GOOS == "darwin" {
		if _, err := lookPath("brew"); err == nil {
			return []string{"brew", "install", "tmux"}, "brew install tmux", nil
		}
		return nil, "", fmt.Errorf("homebrew not found")
	}

	type candidate struct {
		command string
		args    []string
		display string
	}
	for _, cand := range []candidate{
		{command: "apt-get", args: []string{"install", "-y", "tmux"}, display: "apt-get install -y tmux"},
		{command: "dnf", args: []string{"install", "-y", "tmux"}, display: "dnf install -y tmux"},
		{command: "yum", args: []string{"install", "-y", "tmux"}, display: "yum install -y tmux"},
		{command: "pacman", args: []string{"-Sy", "--noconfirm", "tmux"}, display: "pacman -Sy --noconfirm tmux"},
		{command: "apk", args: []string{"add", "tmux"}, display: "apk add tmux"},
		{command: "zypper", args: []string{"--non-interactive", "install", "tmux"}, display: "zypper --non-interactive install tmux"},
	} {
		if _, err := lookPath(cand.command); err == nil {
			return append([]string{cand.command}, cand.args...), cand.display, nil
		}
	}
	return nil, "", fmt.Errorf("no supported package manager found")
}

func withPrivilege(base []string) (string, []string, error) {
	if len(base) == 0 {
		return "", nil, fmt.Errorf("empty install command")
	}
	if runtime.GOOS == "windows" {
		return "", nil, fmt.Errorf("automatic tmux installation is not supported on Windows")
	}
	if geteuid() == 0 || base[0] == "brew" {
		return base[0], base[1:], nil
	}
	if _, err := lookPath("sudo"); err == nil {
		args := append([]string{}, base...)
		return "sudo", args, nil
	}
	return "", nil, fmt.Errorf("sudo not found; install tmux manually")
}

func printTMuxManualInstructions(out io.Writer) {
	_, _ = fmt.Fprintln(out, "  Install tmux manually to use codex.transport=\"tmux\" for interactive sessions.")
	if runtime.GOOS == "darwin" {
		_, _ = fmt.Fprintln(out, "  Example: brew install tmux")
		return
	}
	_, _ = fmt.Fprintln(out, "  Example: sudo apt-get install -y tmux")
	if runtime.GOOS == "windows" {
		_, _ = fmt.Fprintln(out, "  Automatic installation is not supported on Windows hosts.")
	}
}
