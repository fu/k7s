package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/fu/k7s/internal/config"
	"github.com/fu/k7s/internal/resources"
)

// Every color k7s uses, and the text helpers all screens draw with.

var (
	header = lipgloss.NewStyle().Bold(true)
	dim    = lipgloss.NewStyle().Faint(true)
	struck = lipgloss.NewStyle().Faint(true).Strikethrough(true)

	// Set from the palette (config colors).
	badge, green, yellow, amber, red, cyan, flash, menu, menuActive lipgloss.Style
	selectedBG, linkedBG, infoColor                                 lipgloss.Color
)

func init() { setPalette(config.Defaults) }

// setPalette sets the colors: the defaults, or the config's.
func setPalette(c config.Colors) {
	col := func(s string) lipgloss.Color { return lipgloss.Color(s) }
	fg := func(s string) lipgloss.Style { return lipgloss.NewStyle().Foreground(col(s)) }
	green, amber, yellow, red, cyan = fg(c.Good), fg(c.Busy), fg(c.Warn), fg(c.Bad), fg(c.Info)
	infoColor = col(c.Info)
	badge = lipgloss.NewStyle().Bold(true).Foreground(col("0")).Background(infoColor).Padding(0, 1)
	flash = lipgloss.NewStyle().Bold(true).Foreground(col("0")).Background(col(c.Flash))
	menu = lipgloss.NewStyle().Foreground(col("250")).Background(col(c.Menu))
	menuActive = lipgloss.NewStyle().Bold(true).Foreground(col("0")).Background(infoColor)
	selectedBG, linkedBG = col(c.Selected), col(c.Linked)
}

// style is the color of a tone.
func style(t resources.Tone) lipgloss.Style {
	switch t {
	case resources.Dim:
		return dim
	case resources.Good:
		return green
	case resources.Busy:
		return amber
	case resources.Warn:
		return yellow
	case resources.Bad:
		return red
	case resources.Alarm:
		return red.Bold(true)
	case resources.Info:
		return cyan
	case resources.Flash:
		return flash
	case resources.Fresh:
		return green.Bold(true)
	case resources.Strong:
		return header
	}
	return lipgloss.NewStyle()
}

// cells renders cells one after the other, e.g. a status part.
func cells(cs []resources.Cell) string {
	var b strings.Builder
	for _, c := range cs {
		b.WriteString(style(c.Tone).Render(c.Text))
	}
	return b.String()
}

func pad(s string, w int, right bool) string {
	gap := w - lipgloss.Width(s)
	if gap <= 0 {
		return s
	}
	if right {
		return strings.Repeat(" ", gap) + s
	}
	return s + strings.Repeat(" ", gap)
}

// truncate shortens plain text to w cells, ending in "…".
func truncate(s string, w int) string {
	if lipgloss.Width(s) <= w {
		return s
	}
	r := []rune(s)
	for len(r) > 0 && lipgloss.Width(string(r))+1 > w {
		r = r[:len(r)-1]
	}
	return string(r) + "…"
}

// truncateMiddle shortens plain text to w cells as "prefix…suffix".
func truncateMiddle(s string, w int) string {
	r := []rune(s)
	if len(r) <= w || w < 5 {
		return truncate(s, w)
	}
	tail := (w - 1) / 2
	head := w - 1 - tail
	return string(r[:head]) + "…" + string(r[len(r)-tail:])
}

// wrap soft-wraps a (possibly styled) line to width, breaking long words
// too. The first line is indented by first spaces, continuations by rest.
// Every long text (logs, change log, secret values) wraps through here.
func wrap(s string, width, first, rest int) []string {
	avail := width - max(first, rest)
	if avail < 10 {
		return []string{clip(strings.Repeat(" ", first)+s, width)}
	}
	var parts []string
	if lipgloss.Width(s) <= avail {
		parts = []string{s}
	} else {
		parts = strings.Split(lipgloss.NewStyle().Width(avail).Render(s), "\n")
	}
	for i := range parts {
		indent := rest
		if i == 0 {
			indent = first
		}
		parts[i] = strings.Repeat(" ", indent) + strings.TrimRight(parts[i], " ")
	}
	return parts
}

// clip cuts a styled line to w cells without breaking ANSI sequences.
func clip(s string, w int) string { return lipgloss.NewStyle().MaxWidth(w).Render(s) }

// hints renders commands: the part in [brackets] is the key and shows in
// cyan (ideally a letter of the word, as in "s[h]ell"), the rest dim.
func hints(s string) string {
	var b strings.Builder
	for {
		i, j := strings.IndexByte(s, '['), strings.IndexByte(s, ']')
		if i < 0 || j < i {
			b.WriteString(dim.Render(s))
			return b.String()
		}
		b.WriteString(dim.Render(s[:i]) + cyan.Bold(true).Render(s[i+1:j]))
		s = s[j+1:]
	}
}

// joinParts joins status-line parts with a dim separator.
func joinParts(parts []string) string { return strings.Join(parts, dim.Render(" │ ")) }
