// Package style holds metla's colours: Catppuccin Latte on light terminals,
// Mocha on dark ones. Verdicts are always spelled out, so colour is never
// the only signal.
package style

import (
	"fmt"
	"image/color"

	"charm.land/lipgloss/v2"

	"github.com/nklmilojevic/metla/internal/scan"
)

// Styles are the colours one terminal gets.
type Styles struct {
	Title, Accent, Muted, Warn, Err lipgloss.Style
	verdicts                        map[scan.Verdict]lipgloss.Style
}

// New returns the palette for a dark or light background.
func New(dark bool) Styles {
	pick := lipgloss.LightDark(dark)
	c := func(l, d string) color.Color { return pick(lipgloss.Color(l), lipgloss.Color(d)) }
	fg := func(l, d string) lipgloss.Style { return lipgloss.NewStyle().Foreground(c(l, d)) }
	return Styles{
		Title:  fg("#8839ef", "#cba6f7").Bold(true),
		Accent: fg("#8839ef", "#cba6f7"),
		Muted:  fg("#8c8fa1", "#7f849c"),
		Warn:   fg("#df8e1d", "#f9e2af"),
		Err:    fg("#d20f39", "#f38ba8"),
		verdicts: map[scan.Verdict]lipgloss.Style{
			scan.Safe:   fg("#1e66f5", "#89b4fa").Bold(true),
			scan.Review: fg("#df8e1d", "#f9e2af"),
			scan.Unsafe: fg("#d20f39", "#f38ba8").Bold(true),
			scan.Keep:   fg("#8c8fa1", "#7f849c"),
		},
	}
}

// Plain renders everything unstyled, for pipes and tests.
func Plain() Styles {
	s := lipgloss.NewStyle()
	return Styles{Title: s, Accent: s, Muted: s, Warn: s, Err: s, verdicts: map[scan.Verdict]lipgloss.Style{}}
}

// Verdict renders a verdict word.
func (s Styles) Verdict(v scan.Verdict) string {
	if st, ok := s.verdicts[v]; ok {
		return st.Render(string(v))
	}
	return string(v)
}

// Age renders a day count compactly: 5d, 3mo, 2y.
func Age(days int) string {
	switch {
	case days >= 365:
		return fmt.Sprintf("%dy", days/365)
	case days >= 60:
		return fmt.Sprintf("%dmo", days/30)
	}
	return fmt.Sprintf("%dd", days)
}

// Plural picks the word that goes with n.
func Plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
