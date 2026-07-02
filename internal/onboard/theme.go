package onboard

import (
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
)

// Podiom brand palette, mirrored from the web frontend so the wizard matches
// the rest of the product.
const (
	colTeal     = lipgloss.Color("#3f8f7e")
	colTealDeep = lipgloss.Color("#2f6e60")
	colSurface  = lipgloss.Color("#fffdfb")
	colInk      = lipgloss.Color("#2b2520")
	colMuted    = lipgloss.Color("#6f6459")
	colOrange   = lipgloss.Color("#d9663d")
	colGold     = lipgloss.Color("#9a6e1e")
)

var (
	bannerStyle  = lipgloss.NewStyle().Foreground(colSurface).Background(colTealDeep).Bold(true).Padding(0, 2)
	taglineStyle = lipgloss.NewStyle().Foreground(colMuted).Italic(true)
	sectionStyle = lipgloss.NewStyle().Foreground(colTeal).Bold(true)
	dividerStyle = lipgloss.NewStyle().Foreground(colMuted)
	soulBoxStyle = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colTeal).Padding(0, 1)
	noticeStyle  = lipgloss.NewStyle().Foreground(colTeal)
	warnStyle    = lipgloss.NewStyle().Foreground(colOrange)
	goldStyle    = lipgloss.NewStyle().Foreground(colGold)
)

// podiomTheme paints huh's forms in the brand palette. It builds on ThemeBase
// (which has no full-screen background) so the wizard blends into the user's
// terminal instead of painting it cream.
func podiomTheme() *huh.Theme {
	t := huh.ThemeBase()

	t.Focused.Title = t.Focused.Title.Foreground(colTeal).Bold(true)
	t.Focused.Description = t.Focused.Description.Foreground(colMuted)
	t.Focused.SelectSelector = t.Focused.SelectSelector.Foreground(colOrange)
	t.Focused.SelectedOption = t.Focused.SelectedOption.Foreground(colTeal).Bold(true)
	t.Focused.Option = t.Focused.Option.Foreground(colInk)
	t.Focused.NextIndicator = t.Focused.NextIndicator.Foreground(colMuted)
	t.Focused.PrevIndicator = t.Focused.PrevIndicator.Foreground(colMuted)
	t.Focused.FocusedButton = t.Focused.FocusedButton.Foreground(colSurface).Background(colTeal).Bold(true)
	t.Focused.BlurredButton = t.Focused.BlurredButton.Foreground(colMuted)
	t.Focused.ErrorIndicator = t.Focused.ErrorIndicator.Foreground(colOrange)
	t.Focused.ErrorMessage = t.Focused.ErrorMessage.Foreground(colOrange)

	// Dim the inactive fields so the focused one stands out in multi-field groups.
	t.Blurred.Title = t.Blurred.Title.Foreground(colMuted)
	t.Blurred.Description = t.Blurred.Description.Foreground(colMuted)
	t.Blurred.SelectedOption = t.Blurred.SelectedOption.Foreground(colMuted)
	return t
}

// banner announces that onboarding has begun. When clear is true it first wipes
// the screen so any installer/curl output doesn't bleed into the wizard.
func banner(out io.Writer, clear bool) {
	if clear {
		fmt.Fprint(out, "\x1b[2J\x1b[H")
	}
	fmt.Fprintln(out, bannerStyle.Render(" PODIOM "))
	fmt.Fprintln(out, taglineStyle.Render("Let's check your agent CLIs and shape your first agent together."))
}

// section prints a styled header with a divider so each phase of the wizard is
// clearly separated with breathing room.
func section(out io.Writer, title string) {
	fmt.Fprintln(out)
	fmt.Fprintln(out, sectionStyle.Render("▌ "+title))
	fmt.Fprintln(out, dividerStyle.Render(strings.Repeat("─", lipgloss.Width(title)+2)))
}
