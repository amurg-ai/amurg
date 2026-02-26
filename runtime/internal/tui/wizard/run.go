package wizard

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/term"

	"github.com/amurg-ai/amurg/pkg/cli"
	plainwizard "github.com/amurg-ai/amurg/runtime/internal/wizard"
)

// Run launches the TUI wizard. If the terminal is not a TTY (piped, CI, etc.)
// it falls back to the plain-text wizard. Pass plain=true to force the fallback.
func Run(outputPath string, generateSystemd bool, plain bool) (configPath string, startNow bool, err error) {
	if plain || !isTTY() {
		return runPlain(outputPath, generateSystemd)
	}
	return runTUI(outputPath, generateSystemd)
}

func isTTY() bool {
	return term.IsTerminal(int(os.Stdin.Fd()))
}

func runPlain(outputPath string, generateSystemd bool) (string, bool, error) {
	w := plainwizard.New(cli.DefaultPrompter())
	return w.Run(outputPath, generateSystemd)
}

func runTUI(outputPath string, generateSystemd bool) (string, bool, error) {
	m := NewModel(outputPath, generateSystemd)

	p := tea.NewProgram(m, tea.WithAltScreen())
	finalModel, err := p.Run()
	if err != nil {
		return "", false, fmt.Errorf("TUI error: %w", err)
	}

	result := finalModel.(Model).Result()
	if result.Cancelled {
		return "", false, fmt.Errorf("wizard cancelled")
	}

	return result.Path, result.StartNow, nil
}
