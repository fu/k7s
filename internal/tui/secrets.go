package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/fu/k7s/internal/pods"
	"github.com/fu/k7s/internal/secrets"
)

// Secrets are a section of the main page. Opening one fetches its values,
// which stay masked until revealed with r.

type (
	secretMsg struct {
		gen     int
		row     secrets.Row
		deleted bool
	}
	secretsSyncedMsg struct {
		gen int
	}
	secretsErrMsg struct {
		gen int
		err error
	}
	secretDataMsg struct {
		gen  int
		key  string
		data map[string][]byte
		err  error
	}
)

type secretsState struct {
	gen    int
	cancel context.CancelFunc

	rows   map[string]secrets.Row
	order  []string
	synced bool
	err    error
	errAt  time.Time

	// the opened secret
	openKey  string
	openType string
	data     map[string][]byte
	dataErr  error
	reveal   bool
	scroll   int
}

// startSecrets (re)starts watching the secrets of the namespace and filter.
func (m *model) startSecrets() {
	s := &m.secrets
	if s.cancel != nil {
		s.cancel()
	}
	gen := s.gen + 1
	ctx, cancel := context.WithCancel(m.ctx)
	*s = secretsState{gen: gen, cancel: cancel, rows: map[string]secrets.Row{}}
	send := m.send
	err := secrets.Watch(ctx, m.client, m.info.Namespace, m.info.Selector, secrets.Handlers{
		Row:    func(r secrets.Row, deleted bool) { send(secretMsg{gen, r, deleted}) },
		Synced: func() { send(secretsSyncedMsg{gen}) },
		Error:  func(err error) { send(secretsErrMsg{gen, err}) },
	})
	if err != nil {
		s.err, s.errAt = err, time.Now()
	}
}

func (m *model) secretsMsg(msg tea.Msg) {
	s := &m.secrets
	switch msg := msg.(type) {
	case secretMsg:
		if msg.gen != s.gen {
			return
		}
		key := msg.row.Key()
		old, existed := s.rows[key]
		if msg.deleted {
			delete(s.rows, key)
		} else {
			s.rows[key] = msg.row
		}
		s.resort()
		if s.synced {
			m.logSecretChange(old, existed, msg.row, msg.deleted)
		}
	case secretsSyncedMsg:
		if msg.gen == s.gen {
			s.synced = true
		}
	case secretsErrMsg:
		if msg.gen == s.gen {
			s.err, s.errAt = msg.err, time.Now()
		}
	case secretDataMsg:
		if msg.gen == s.gen && msg.key == s.openKey {
			s.data, s.dataErr = msg.data, msg.err
		}
	}
}

// logSecretChange records added, deleted and updated secrets (an update is a
// new resourceVersion; the values themselves are never looked at).
func (m *model) logSecretChange(old secrets.Row, existed bool, r secrets.Row, deleted bool) {
	line := eventLine{at: time.Now(), name: "secret/" + r.Name}
	switch {
	case deleted:
		line.kind = pods.Deleted
	case !existed:
		line.kind, line.detail = pods.Added, fmt.Sprintf("%s · %d keys", r.Type, r.Keys)
	case old.Version != r.Version:
		line.kind, line.detail = pods.Modified, "data updated"
		if old.Keys != r.Keys {
			line.detail = fmt.Sprintf("keys %d → %d", old.Keys, r.Keys)
		}
	default:
		return
	}
	m.pods.logEvent(line)
}

func (s *secretsState) resort() {
	s.order = s.order[:0]
	for k := range s.rows {
		s.order = append(s.order, k)
	}
	sort.Strings(s.order)
}

// openSecret shows one secret's keys (values masked).
func (m *model) openSecret(row secrets.Row) tea.Cmd {
	s := &m.secrets
	s.openKey, s.openType = row.Key(), row.Type
	s.data, s.dataErr, s.reveal, s.scroll = nil, nil, false, 0
	m.screen = screenSecret
	gen, client, ctx := s.gen, m.client, m.ctx
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		data, err := secrets.Values(ctx, client, row.Namespace, row.Name)
		return secretDataMsg{gen: gen, key: row.Key(), data: data, err: err}
	}
}

func (m *model) secretKey(msg tea.KeyMsg) tea.Cmd {
	s := &m.secrets
	switch msg.String() {
	case "esc":
		s.data, s.openKey, s.reveal = nil, "", false
		m.screen = screenMain
	case "r":
		s.reveal = !s.reveal
	case "up", "k":
		s.scroll = max(0, s.scroll-1)
	case "down", "j":
		s.scroll++
	case "pgup":
		s.scroll = max(0, s.scroll-(m.height-4))
	case "pgdown":
		s.scroll += m.height - 4
	}
	return nil
}

// ---- rendering -----------------------------------------------------------

// secretsTable is the secrets section's table, in s.order.
func (m *model) secretsTable() table {
	s := &m.secrets
	t := table{
		headers: []string{"#", "NAME", "TYPE", "KEYS", "AGE"},
		right:   []bool{true, false, false, true, false},
		flex:    1,
	}
	for i, k := range s.order {
		r := s.rows[k]
		t.rows = append(t.rows, []cell{
			{text: fmt.Sprint(i + 1), style: stDim},
			{text: r.Name, middle: true},
			{text: r.Type, style: stDim},
			{text: fmt.Sprint(r.Keys)},
			{text: pods.Age(r.Created), style: stDim},
		})
	}
	return t
}

func (m *model) secretView() string {
	s := &m.secrets
	head := m.crumbs(m.info.Context, stYellow.Render("secret "+s.openKey), stDim.Render(s.openType))

	var lines []string
	switch {
	case s.dataErr != nil:
		lines = append(lines, stRed.Render("  error: "+s.dataErr.Error()))
	case s.data == nil:
		lines = append(lines, stDim.Render("  loading…"))
	case len(s.data) == 0:
		lines = append(lines, stDim.Render("  no keys"))
	}
	keys := make([]string, 0, len(s.data))
	for k := range s.data {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := s.data[k]
		lines = append(lines, "  "+stHeader.Render(k)+stDim.Render(fmt.Sprintf("  %d bytes", len(v))))
		switch {
		case !s.reveal:
			lines = append(lines, stDim.Render("    ••••••••"))
		case !utf8.Valid(v):
			lines = append(lines, stDim.Render(fmt.Sprintf("    <binary, %d bytes>", len(v))))
		case len(v) == 0:
			lines = append(lines, stDim.Render("    (empty)"))
		default:
			for _, l := range strings.Split(strings.TrimRight(string(v), "\n"), "\n") {
				lines = append(lines, wrap(l, m.width, 4, 4)...)
			}
		}
	}

	bodyH := max(1, m.height-3)
	s.scroll = max(0, min(s.scroll, len(lines)-bodyH))
	lines = lines[s.scroll:min(len(lines), s.scroll+bodyH)]
	for len(lines) < bodyH {
		lines = append(lines, "")
	}
	cmd := "[r]eveal"
	if s.reveal {
		cmd = "hide [r]"
	}
	return head + "\n\n" + strings.Join(lines, "\n") + "\n" + m.footer(cmd+"  [esc] back")
}
