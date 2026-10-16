package tui

import (
	"context"
	"fmt"
	"slices"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/fu/k7s/internal/resources"
)

// The picker screen: one table to choose a row from, for every Picker step
// (a container, a port) and the scope menus (context, namespace, filter).

type (
	pickerLoadedMsg struct {
		s    *pickerScreen
		rows []resources.Row
		err  error
	}
	pickerReloadMsg struct{ s *pickerScreen }
)

type pickerScreen struct {
	m        *model
	p        *resources.Picker
	scope    string // the scope menu it belongs to, if any
	rows     []resources.Row
	loaded   bool
	loadedAt time.Time
	err      error
	closed   bool // stops reloading
	cursor   string
	offset   int
	page     int
}

func (m *model) openPicker(p *resources.Picker) tea.Cmd {
	s := &pickerScreen{m: m, p: p, rows: p.Rows, loaded: p.Load == nil, cursor: p.Current, page: 10}
	m.open(s)
	if p.Load != nil {
		return s.load()
	}
	return nil
}

func (s *pickerScreen) load() tea.Cmd {
	ctx, load := s.m.ctx, s.p.Load
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		rows, err := load(ctx)
		return pickerLoadedMsg{s, rows, err}
	}
}

func (s *pickerScreen) hints() string {
	what := s.p.Hint
	if what == "" {
		what = "choose"
	}
	return hints("[enter] " + what + "  [esc] back")
}

func (s *pickerScreen) update(msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case pickerLoadedMsg:
		if msg.s != s || s.closed {
			return nil, false
		}
		s.err, s.loadedAt, s.loaded = msg.err, time.Now(), true
		if msg.err == nil || len(s.rows) == 0 {
			s.rows = msg.rows
		}
		if s.p.Every > 0 {
			return tea.Tick(s.p.Every, func(time.Time) tea.Msg { return pickerReloadMsg{s} }), false
		}
	case pickerReloadMsg:
		if msg.s == s && !s.closed {
			return s.load(), false
		}
	case tea.KeyMsg:
		rows := s.all()
		i := slices.IndexFunc(rows, func(r resources.Row) bool { return r.Key == s.cursor })
		switch msg.String() {
		case "esc":
			s.closed = true
			return nil, true
		case "enter", " ":
			if i < 0 {
				return nil, false
			}
			step := s.p.Pick(s.cursor)
			if _, stay := step.(resources.Stay); stay {
				return nil, false
			}
			cmd, opened := s.m.follow(step)
			s.closed = !opened
			return cmd, !opened
		default:
			if j := moveCursor(msg.String(), max(0, i), len(rows), s.page); j < len(rows) {
				s.cursor = rows[j].Key
			}
		}
	}
	return nil, false
}

// all are the loaded (or given) rows and the live ones.
func (s *pickerScreen) all() []resources.Row {
	if s.p.Live == nil {
		return s.rows
	}
	return append(slices.Clip(s.rows), s.p.Live()...)
}

func (s *pickerScreen) view(width, height int) string {
	rows := s.all()
	if !slices.ContainsFunc(rows, func(r resources.Row) bool { return r.Key == s.cursor }) && len(rows) > 0 {
		s.cursor = rows[0].Key
	}
	title := yellow.Render(s.p.Title)
	switch {
	case s.err != nil:
		title += dim.Render(" │ ") + red.Render("error: "+s.err.Error())
	case !s.loaded:
		title += dim.Render(" │ loading…")
	case len(rows) == 0 && s.p.Empty != "":
		title += dim.Render(" │ ") + amber.Render(s.p.Empty)
	case s.p.Every > 0:
		title += dim.Render(fmt.Sprintf(" │ refreshed %ds ago", int(time.Since(s.loadedAt).Seconds())))
	}
	s.page = max(1, height-4)
	t := table{cols: s.p.Columns, rows: rows}
	return clip(title, width) + "\n\n" + t.render(s.cursor, &s.offset, height-2, width)
}

// moveCursor handles the list keys every list shares.
func moveCursor(key string, cursor, n, page int) int {
	switch key {
	case "up", "k":
		return clamp(cursor, -1, n)
	case "down", "j":
		return clamp(cursor, 1, n)
	case "pgup":
		return clamp(cursor, -page, n)
	case "pgdown":
		return clamp(cursor, page, n)
	case "home":
		return 0
	case "end", "G":
		return max(0, n-1)
	}
	return cursor
}
