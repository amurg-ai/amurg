// Package tui provides shared theme and styles for the runtime TUI.
package tui

import "github.com/charmbracelet/lipgloss"

// Colors — brand palette.
var (
	ColorPrimary   = lipgloss.Color("#7C3AED") // violet
	ColorSecondary = lipgloss.Color("#6366F1") // indigo
	ColorAccent    = lipgloss.Color("#F59E0B") // amber

	ColorSuccess = lipgloss.Color("#10B981") // emerald
	ColorWarning = lipgloss.Color("#F59E0B") // amber
	ColorError   = lipgloss.Color("#EF4444") // red
	ColorMuted   = lipgloss.Color("#6B7280") // gray-500
	ColorText    = lipgloss.Color("#E5E7EB") // gray-200
	ColorSubtle  = lipgloss.Color("#9CA3AF") // gray-400
)

// Shared styles used across wizard and dashboard.
var (
	// Title is the main heading style (e.g. wizard step title, dashboard header).
	Title = lipgloss.NewStyle().
		Bold(true).
		Foreground(ColorPrimary).
		MarginBottom(1)

	// Subtitle for secondary headings.
	Subtitle = lipgloss.NewStyle().
			Bold(true).
			Foreground(ColorSecondary)

	// Description for helper text.
	Description = lipgloss.NewStyle().
			Foreground(ColorSubtle)

	// Selected highlights the currently focused item.
	Selected = lipgloss.NewStyle().
			Foreground(ColorPrimary).
			Bold(true)

	// Dimmed for non-focused items.
	Dimmed = lipgloss.NewStyle().
		Foreground(ColorMuted)

	// Success for positive messages.
	Success = lipgloss.NewStyle().
		Foreground(ColorSuccess)

	// ErrorStyle for error messages (avoiding collision with builtin error).
	ErrorStyle = lipgloss.NewStyle().
			Foreground(ColorError)

	// WarningStyle for warning messages.
	WarningStyle = lipgloss.NewStyle().
			Foreground(ColorWarning)

	// Help for keybind hints at the bottom.
	Help = lipgloss.NewStyle().
		Foreground(ColorMuted)

	// Border is a rounded border style for panels.
	Border = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorPrimary).
		Padding(0, 1)

	// CodeBox for displaying codes, paths, etc.
	CodeBox = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorAccent).
		Padding(1, 2).
		Align(lipgloss.Center)

	// ActiveDot represents connected status.
	ActiveDot = lipgloss.NewStyle().
			Foreground(ColorSuccess).
			Render("●")

	// InactiveDot represents disconnected status.
	InactiveDot = lipgloss.NewStyle().
			Foreground(ColorError).
			Render("●")

	// WarnDot represents reconnecting status.
	WarnDot = lipgloss.NewStyle().
		Foreground(ColorWarning).
		Render("●")
)

// StatusDot returns a colored dot for hub connection status.
func StatusDot(connected bool, reconnecting bool) string {
	if reconnecting {
		return WarnDot
	}
	if connected {
		return ActiveDot
	}
	return InactiveDot
}

// StatusText returns a colored status label.
func StatusText(connected bool, reconnecting bool) string {
	if reconnecting {
		return WarningStyle.Render("reconnecting")
	}
	if connected {
		return Success.Render("connected")
	}
	return ErrorStyle.Render("disconnected")
}

// LogLevelStyle returns a style for the given log level.
func LogLevelStyle(level string) lipgloss.Style {
	switch level {
	case "DEBUG":
		return lipgloss.NewStyle().Foreground(ColorMuted)
	case "INFO":
		return lipgloss.NewStyle().Foreground(ColorSuccess)
	case "WARN":
		return lipgloss.NewStyle().Foreground(ColorWarning)
	case "ERROR":
		return lipgloss.NewStyle().Foreground(ColorError)
	default:
		return lipgloss.NewStyle().Foreground(ColorText)
	}
}
