package dashboard

import (
	"github.com/charmbracelet/lipgloss"

	"github.com/amurg-ai/amurg/runtime/internal/tui"
)

type helpModel struct {
	visible bool
}

func newHelp() helpModel {
	return helpModel{}
}

func (h *helpModel) toggle() {
	h.visible = !h.visible
}

func (h helpModel) bar() string {
	return tui.Help.Render("  q quit  d detach  Tab switch  j/k navigate  G bottom  ? help")
}

func (h helpModel) View() string {
	title := tui.Title.Render("Keyboard Shortcuts") + "\n\n"

	binds := []struct {
		key  string
		desc string
	}{
		{"q / Ctrl+C", "Quit (stops daemon if inline)"},
		{"d / Ctrl+D", "Detach (leave daemon running)"},
		{"Tab", "Switch between Sessions and Logs panels"},
		{"j / Down", "Move down / scroll down"},
		{"k / Up", "Move up / scroll up"},
		{"G", "Jump to bottom (logs)"},
		{"g", "Jump to top"},
		{"/", "Filter (not yet implemented)"},
		{"?", "Toggle this help"},
	}

	keyStyle := lipgloss.NewStyle().
		Foreground(tui.ColorAccent).
		Bold(true).
		Width(14)

	descStyle := lipgloss.NewStyle().
		Foreground(tui.ColorText)

	s := title
	for _, b := range binds {
		s += "  " + keyStyle.Render(b.key) + descStyle.Render(b.desc) + "\n"
	}
	s += "\n" + tui.Help.Render("  Press ? to close")

	return lipgloss.NewStyle().Padding(1, 2).Render(s)
}
