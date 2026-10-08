// Package ui holds the lipgloss styles tackroom's terminal reports share and
// prints them with only as much color as the destination can show.
package ui

import (
	"io"
	"os"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/term"
)

var (
	Bold   = lipgloss.NewStyle().Bold(true)
	Dim    = lipgloss.NewStyle().Faint(true)
	Green  = lipgloss.NewStyle().Foreground(lipgloss.Green)
	Yellow = lipgloss.NewStyle().Foreground(lipgloss.Yellow)
	Red    = lipgloss.NewStyle().Foreground(lipgloss.Red)
)

// Mark returns the status glyph for "ok", "warn", or anything else (fail).
func Mark(kind string) string {
	switch kind {
	case "ok":
		return Green.Render("✓")
	case "warn":
		return Yellow.Render("!")
	default:
		return Red.Render("✗")
	}
}

// Writer wraps w so writes carry only what w can show: no escape codes when
// w is not a terminal, and no color when NO_COLOR is set. Any non-empty
// NO_COLOR counts (https://no-color.org); colorprofile on its own honors only
// boolean values. Detection runs once, so reuse the writer for repeated output.
func Writer(w io.Writer) io.Writer {
	cw := colorprofile.NewWriter(w, os.Environ())
	if os.Getenv("NO_COLOR") != "" && cw.Profile > colorprofile.ASCII {
		cw.Profile = colorprofile.ASCII
	}
	return cw
}

// Fprint writes s to w through Writer.
func Fprint(w io.Writer, s string) error {
	_, err := io.WriteString(Writer(w), s)
	return err
}

// Width is the column count to wrap a report for w: the terminal width, or 100
// when w is not a terminal.
func Width(w io.Writer) int {
	if f, ok := w.(interface{ Fd() uintptr }); ok && term.IsTerminal(f.Fd()) {
		if cols, _, err := term.GetSize(f.Fd()); err == nil && cols > 0 {
			return cols
		}
	}
	return 100
}
