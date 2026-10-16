package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"k7s/internal/pods"
	"k7s/internal/secrets"
)

// The secrets view lists secrets live. Opening one fetches its values, which
// stay masked until revealed with r.

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

	rows           map[string]secrets.Row
	order          []string
	selected       string
	cursor, offset int
	jumpBuf        string
	jumpAt         time.Time
	synced         bool
	err            error
	errAt          time.Time

	// the opened secret
	openKey string
	data    map[string][]byte
	dataErr error
	reveal  bool
	scroll  int
}

// openSecrets starts watching the secrets of the current scope.
func (m *model) openSecrets() tea.Cmd {
	s := &m.secrets
	if s.cancel != nil {
		s.cancel()
	}
	gen := s.gen + 1
	ctx, cancel := context.WithCancel(m.ctx)
	*s = secretsState{gen: gen, cancel: cancel, rows: map[string]secrets.Row{}}
	m.screen, m.lastView = screenSecrets, screenSecrets

	ns := m.info.Namespace
	if m.info.AllNamespaces {
		ns = metav1.NamespaceAll
	}
	send := m.send
	err := secrets.Watch(ctx, m.client, ns, secrets.Handlers{
		Row:    func(r secrets.Row, deleted bool) { send(secretMsg{gen, r, deleted}) },
		Synced: func() { send(secretsSyncedMsg{gen}) },
		Error:  func(err error) { send(secretsErrMsg{gen, err}) },
	})
	if err != nil {
		s.err, s.errAt = err, time.Now()
	}
	return nil
}

func (m *model) closeSecrets() {
	s := &m.secrets
	if s.cancel != nil {
		s.cancel()
	}
	s.gen++
	s.data, s.openKey, s.reveal = nil, "", false
}

func (m *model) secretsMsg(msg tea.Msg) {
	s := &m.secrets
	switch msg := msg.(type) {
	case secretMsg:
		if msg.gen != s.gen {
			return
		}
		if msg.deleted {
			delete(s.rows, msg.row.Key())
		} else {
			s.rows[msg.row.Key()] = msg.row
		}
		s.resort()
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

func (s *secretsState) resort() {
	s.order = s.order[:0]
	for k := range s.rows {
		s.order = append(s.order, k)
	}
	sort.Strings(s.order)
	s.cursor = 0
	for i, k := range s.order {
		if k == s.selected {
			s.cursor = i
		}
	}
	if len(s.order) > 0 {
		s.selected = s.order[s.cursor]
	}
}

func (s *secretsState) move(delta int) {
	if len(s.order) == 0 {
		return
	}
	s.cursor = clampCursor(s.cursor, delta, len(s.order))
	s.selected = s.order[s.cursor]
}

func (m *model) secretsKey(msg tea.KeyMsg) tea.Cmd {
	s := &m.secrets
	page := max(1, m.height-4)
	key := msg.String()
	if isDigit(key) {
		if n := typeNumber(&s.jumpBuf, &s.jumpAt, key); n >= 1 && n <= len(s.order) {
			s.cursor, s.selected = n-1, s.order[n-1]
		}
		return nil
	}
	switch key {
	case "esc":
		m.closeSecrets()
		m.screen, m.lastView = screenPods, screenPods
	case "up", "k":
		s.move(-1)
	case "down", "j":
		s.move(1)
	case "pgup":
		s.move(-page)
	case "pgdown":
		s.move(page)
	case "home":
		s.move(-len(s.order))
	case "end", "G":
		s.move(len(s.order))
	case "enter":
		row, ok := s.rows[s.selected]
		if !ok {
			return nil
		}
		s.openKey, s.data, s.dataErr, s.reveal, s.scroll = row.Key(), nil, nil, false, 0
		m.screen = screenSecret
		gen, client, ctx := s.gen, m.client, m.ctx
		return func() tea.Msg {
			ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()
			data, err := secrets.Values(ctx, client, row.Namespace, row.Name)
			return secretDataMsg{gen: gen, key: row.Key(), data: data, err: err}
		}
	}
	return nil
}

func (m *model) secretKey(msg tea.KeyMsg) tea.Cmd {
	s := &m.secrets
	switch msg.String() {
	case "esc":
		s.data, s.openKey, s.reveal = nil, "", false
		m.screen = screenSecrets
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

func (m *model) secretsStatusLine() string {
	s := &m.secrets
	parts := []string{
		stBadge.Render("k7s"),
		"ctx " + stCyan.Render(m.info.Context),
		"ns " + stCyan.Render(m.nsLabel()),
		fmt.Sprintf("secrets %d", len(s.rows)),
	}
	switch {
	case s.err != nil && time.Since(s.errAt) < 30*time.Second:
		parts = append(parts, stRed.Render("watch error: "+s.err.Error()))
	case s.synced:
		parts = append(parts, m.pulse()+" "+stCyan.Render("watching"))
	default:
		parts = append(parts, stYellow.Render("○ syncing"))
	}
	return clip(strings.Join(parts, stDim.Render(" │ ")), m.width)
}

func (m *model) secretsView() string {
	s := &m.secrets
	t := table{
		headers: []string{"#", "NAME", "TYPE", "KEYS", "AGE"},
		right:   []bool{true, false, false, true, false},
		flex:    1,
	}
	if m.info.AllNamespaces {
		t.headers = []string{"#", "NAMESPACE", "NAME", "TYPE", "KEYS", "AGE"}
		t.right = []bool{true, false, false, false, true, false}
		t.flex = 2
	}
	for i, k := range s.order {
		r := s.rows[k]
		cells := []cell{
			{text: fmt.Sprint(i + 1), style: stDim},
			{text: r.Name, middle: true},
			{text: r.Type, style: stDim},
			{text: fmt.Sprint(r.Keys)},
			{text: pods.Age(r.Created), style: stDim},
		}
		if m.info.AllNamespaces {
			cells = append([]cell{cells[0], {text: r.Namespace, style: stDim}}, cells[1:]...)
		}
		t.rows = append(t.rows, cells)
	}
	body := t.render(s.cursor, &s.offset, m.height-3, m.width)
	return m.secretsStatusLine() + "\n\n" + body +
		m.footer(jumpOr(s.jumpBuf, s.jumpAt, "[enter] open  "+m.scopeHint()+"  [esc] back"))
}

func (m *model) secretView() string {
	s := &m.secrets
	row := s.rows[s.openKey]
	head := m.crumbs(m.info.Context, stYellow.Render("secret "+s.openKey), stDim.Render(row.Type))

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
				lines = append(lines, clip("    "+l, m.width))
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
