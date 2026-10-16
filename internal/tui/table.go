package tui

import (
	"fmt"
	"slices"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/fu/k7s/internal/resources"
)

// table lays out what a kind (or a picker) says to show: column widths,
// shrinking and dropping columns on narrow terminals, truncation, tones and
// the selected row. Sections get a "#" column for jumping to a row.

type table struct {
	cols   []resources.Column
	rows   []resources.Row
	number bool                   // prepend the # column
	linked map[resources.Ref]bool // rows linked to the selected one, tinted
}

const sep = "  "

// lines renders the header and one line per row, the one with key
// selected highlighted.
func (t table) lines(selected string, width int) (string, []string) {
	cols, rows := t.cols, t.rows
	if t.number {
		cols = append([]resources.Column{{Title: "#", Right: true}}, cols...)
		numbered := make([]resources.Row, len(rows))
		for i, r := range rows {
			numbered[i] = r
			numbered[i].Cells = append([]resources.Cell{{Text: fmt.Sprint(i + 1), Tone: resources.Dim}}, r.Cells...)
		}
		rows = numbered
	}

	widths := make([]int, len(cols))
	for i, c := range cols {
		widths[i] = lipgloss.Width(c.Title)
		for _, r := range rows {
			if i < len(r.Cells) {
				widths[i] = max(widths[i], lipgloss.Width(r.Cells[i].Text))
			}
		}
	}
	shown := fit(cols, widths, width)

	hdr := make([]resources.Cell, len(cols))
	for i, c := range cols {
		hdr[i] = resources.Cell{Text: c.Title, Tone: resources.Strong}
	}
	out := make([]string, len(rows))
	for i, r := range rows {
		var bg lipgloss.TerminalColor
		switch {
		case r.Key == selected:
			bg = selectedBG
		case t.linked[r.Ref]:
			bg = linkedBG
		}
		out[i] = t.line(cols, widths, shown, r.Cells, r.Struck, bg, width)
	}
	return t.line(cols, widths, shown, hdr, false, nil, width), out
}

// fit makes the columns fit width: shrink the shrinkable ones (to 32 cells),
// then drop the droppable ones, then shrink further (to 8). It returns which
// columns are shown and adjusts widths in place.
func fit(cols []resources.Column, widths []int, width int) []bool {
	shown := make([]bool, len(cols))
	for i := range shown {
		shown[i] = true
	}
	total := func() int {
		t := -len(sep)
		for i, w := range widths {
			if shown[i] {
				t += w + len(sep)
			}
		}
		return t
	}
	shrink := func(floor int) {
		for i, c := range cols {
			if over := total() - width; over > 0 && c.Shrink && shown[i] {
				widths[i] = max(min(floor, widths[i]), widths[i]-over)
			}
		}
	}
	shrink(32)
	var droppable []int
	for i, c := range cols {
		if c.Drop > 0 {
			droppable = append(droppable, i)
		}
	}
	slices.SortStableFunc(droppable, func(a, b int) int { return cols[a].Drop - cols[b].Drop })
	for _, i := range droppable {
		if total() <= width {
			break
		}
		shown[i] = false
	}
	shrink(8)
	return shown
}

// line renders one row; bg (selected or linked) fills the whole line.
func (t table) line(cols []resources.Column, widths []int, shown []bool, cs []resources.Cell, isStruck bool, bg lipgloss.TerminalColor, width int) string {
	var parts []string
	for i, c := range cols {
		if !shown[i] {
			continue
		}
		var cell resources.Cell
		if i < len(cs) {
			cell = cs[i]
		}
		text := truncate(cell.Text, widths[i])
		if c.Middle {
			text = truncateMiddle(cell.Text, widths[i])
		}
		st := style(cell.Tone)
		if isStruck {
			st = struck
		}
		if bg != nil {
			st = st.Background(bg)
		}
		parts = append(parts, st.Render(pad(text, widths[i], c.Right)))
	}
	if bg == nil {
		return clip(strings.Join(parts, sep), width)
	}
	fill := lipgloss.NewStyle().Background(bg)
	line := clip(strings.Join(parts, fill.Render(sep)), width)
	if rest := width - lipgloss.Width(line); rest > 0 {
		line += fill.Render(strings.Repeat(" ", rest))
	}
	return line
}

// render writes the header plus exactly height-1 row lines, scrolled so the
// selected row is visible.
func (t table) render(selected string, offset *int, height, width int) string {
	header, lines := t.lines(selected, width)
	cursor := slices.IndexFunc(t.rows, func(r resources.Row) bool { return r.Key == selected })
	rows := max(1, height-1)
	*offset = scrollOffset(max(0, cursor), *offset, rows, len(lines))
	var b strings.Builder
	b.WriteString(header + "\n")
	for i := 0; i < rows; i++ {
		if idx := *offset + i; idx < len(lines) {
			b.WriteString(lines[idx])
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// scrollOffset keeps cursor within a window of rows lines over n lines.
func scrollOffset(cursor, offset, rows, n int) int {
	if cursor < offset {
		offset = cursor
	}
	if cursor >= offset+rows {
		offset = cursor - rows + 1
	}
	return max(0, min(offset, max(0, n-rows)))
}

// clamp moves cursor by delta within [0, n).
func clamp(cursor, delta, n int) int {
	if n == 0 {
		return 0
	}
	return max(0, min(n-1, cursor+delta))
}
