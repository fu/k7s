package tui

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/fu/k7s/internal/resources"
)

// The filter is a scope like context and namespace: a label selector that
// applies to every view (pods, secrets, overview). Its pane suggests the
// selectors found in the current namespace, plus "everything".

type selCandidatesMsg struct {
	gen   int
	cands []resources.Candidate
	err   error
}

type filterState struct {
	gen            int
	cands          []resources.Candidate
	err            error
	loaded         bool
	cursor, offset int // row 0 is "everything"
}

func (m *model) openFilter() tea.Cmd {
	f := &m.filter
	*f = filterState{gen: f.gen + 1}
	m.screen = screenFilter
	gen, client, ctx, ns := f.gen, m.client, m.ctx, m.info.Namespace
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		cands, err := resources.Selectors(ctx, client, ns)
		return selCandidatesMsg{gen, cands, err}
	}
}

func (m *model) filterMsg(msg selCandidatesMsg) {
	f := &m.filter
	if msg.gen != f.gen {
		return
	}
	f.cands, f.err, f.loaded = msg.cands, msg.err, true
	for i, c := range f.cands {
		if c.Selector == m.info.Selector {
			f.cursor = i + 1
		}
	}
}

func (m *model) filterKey(msg tea.KeyMsg) tea.Cmd {
	f := &m.filter
	n := len(f.cands) + 1
	switch msg.String() {
	case "esc":
		f.gen++
		m.screen = screenMain
	case "up", "k":
		f.cursor = clampCursor(f.cursor, -1, n)
	case "down", "j":
		f.cursor = clampCursor(f.cursor, 1, n)
	case "pgup":
		f.cursor = clampCursor(f.cursor, -(m.height - 4), n)
	case "pgdown":
		f.cursor = clampCursor(f.cursor, m.height-4, n)
	case "home":
		f.cursor = 0
	case "end", "G":
		f.cursor = n - 1
	case "enter":
		sel := ""
		if f.cursor > 0 {
			sel = f.cands[f.cursor-1].Selector
		}
		f.gen++
		m.screen = screenMain
		if sel != m.info.Selector {
			m.info.Selector = sel
			m.scopeChanged()
		}
	}
	return nil
}

func upper(ss []string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = strings.ToUpper(s)
	}
	return out
}

func (m *model) filterView() string {
	f := &m.filter
	kinds := resources.Kinds()
	t := table{
		headers: append([]string{" ", "FILTER"}, upper(kinds)...),
		right:   append([]bool{false, false}, slices.Repeat([]bool{true}, len(kinds))...),
		flex:    1,
	}
	marker := func(sel string) cell {
		if sel == m.info.Selector {
			return cell{text: "●", style: stGreen}
		}
		return cell{text: " "}
	}
	all := []cell{marker(""), {text: "everything (no filter)", style: stCyan}}
	for range kinds {
		all = append(all, cell{})
	}
	t.rows = append(t.rows, all)
	for _, c := range f.cands {
		row := []cell{marker(c.Selector), {text: c.Selector}}
		for _, k := range kinds {
			count := cell{text: fmt.Sprint(c.Counts[k]), style: stHeader}
			if c.Counts[k] == 0 {
				count.style = stDim
			}
			row = append(row, count)
		}
		t.rows = append(t.rows, row)
	}

	state := stDim.Render("reading labels…")
	switch {
	case f.err != nil:
		state = stRed.Render("error: " + f.err.Error())
	case f.loaded && len(f.cands) == 0:
		state = stAmber.Render("no app labels found here")
	case f.loaded:
		state = stDim.Render(fmt.Sprintf("%d label selectors", len(f.cands)))
	}
	head := m.crumbs(m.info.Context, stYellow.Render("filter"), "ns "+stCyan.Render(m.info.Namespace), state)
	body := t.render(f.cursor, &f.offset, m.height-3, m.width)
	return head + "\n\n" + body + m.footer("[enter] apply  [esc] back")
}
