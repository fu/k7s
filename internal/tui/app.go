// Package tui renders k7s: one main page for a namespace (and filter) with
// its resources in sections and a change log underneath. The navigation bar
// holds the scope menus (contexts, namespaces, filter) and the section
// toggles (pods, secrets, gitops); sub screens (logs, shell, secret, pickers)
// return to the main page with esc.
package tui

import (
	"context"
	"errors"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"k8s.io/cli-runtime/pkg/genericclioptions"

	"github.com/fu/k7s/internal/kube"
)

// Info describes what is being watched.
type Info struct {
	Context   string
	Server    string
	Namespace string
	Selector  string
	Refresh   time.Duration
	Notice    string // shown in the navigation bar at start
}

type screen int

const (
	screenMain screen = iota
	screenContainers
	screenLogs
	screenNamespaces
	screenContexts
	screenFilter
	screenSecret // one secret's keys
	screenPorts  // pick a port to forward
)

// scopes are the menus that change what is shown: each opens a picker.
var scopes = []struct {
	name   string
	screen screen
}{
	{"contexts", screenContexts},
	{"namespaces", screenNamespaces},
	{"filter", screenFilter},
}

type tickMsg time.Time

type model struct {
	ctx      context.Context
	startCmd tea.Cmd
	flags    *genericclioptions.ConfigFlags // to build clients for other contexts
	client   *kube.Client                   // the context in use
	send     func(tea.Msg)
	info     Info

	screen        screen
	width, height int
	notice        string
	noticeAt      time.Time

	main       mainState
	pods       podsState
	secrets    secretsState
	gitops     gitopsState
	containers containersState
	logs       logsState
	namespaces nsState
	contexts   ctxState
	filter     filterState
	ports      portsState
	forwards   map[string]*forward // active port-forwards by pod/port
}

// Run shows the main page and blocks until the user quits.
func Run(ctx context.Context, flags *genericclioptions.ConfigFlags, client *kube.Client, info Info) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	m := &model{ctx: ctx, flags: flags, client: client, info: info, forwards: map[string]*forward{}}
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithContext(ctx))
	m.send = p.Send
	m.startPods()
	m.startSecrets()
	m.startCmd = m.startGitOps()
	if info.Notice != "" {
		m.setNotice(info.Notice)
		m.noticeAt = time.Now().Add(4 * time.Second) // a little longer at start
	}

	if _, err := p.Run(); err != nil && !errors.Is(err, tea.ErrProgramKilled) {
		return err
	}
	return nil
}

// pulseMsg redraws the "watching" dot often enough for a smooth fade.
type pulseMsg struct{}

func tick() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func pulseTick() tea.Cmd {
	return tea.Tick(80*time.Millisecond, func(time.Time) tea.Msg { return pulseMsg{} })
}

func (m *model) Init() tea.Cmd { return tea.Batch(tick(), pulseTick(), m.startCmd) }

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		if cmd, ok := m.globalKey(msg.String()); ok {
			return m, cmd
		}
		switch m.screen {
		case screenContainers:
			return m, m.containersKey(msg)
		case screenLogs:
			return m, m.logsKey(msg)
		case screenNamespaces:
			return m, m.namespacesKey(msg)
		case screenContexts:
			return m, m.contextsKey(msg)
		case screenFilter:
			return m, m.filterKey(msg)
		case screenSecret:
			return m, m.secretKey(msg)
		case screenPorts:
			return m, m.portsKey(msg)
		default:
			return m, m.mainKey(msg)
		}
	case tickMsg:
		m.pods.purgeDeleted()
		m.gitops.resort() // drops deleted apps after a while
		return m, tick()
	case pulseMsg:
		return m, pulseTick()
	case podMsg, metricsMsg, syncedMsg, watchErrMsg, kubeEventMsg:
		m.podsMsg(msg)
	case secretMsg, secretsSyncedMsg, secretsErrMsg, secretDataMsg:
		m.secretsMsg(msg)
	case gitopsDetectedMsg, appMsg, appsSyncedMsg, appsErrMsg:
		m.gitopsMsg(msg)
	case shellDoneMsg:
		if msg.err != nil {
			m.setNotice("shell: " + msg.err.Error())
		}
	case logLineMsg, logEndMsg:
		m.logsMsg(msg)
	case nsLoadedMsg, nsRefreshMsg:
		return m, m.namespacesMsg(msg)
	case fwdReadyMsg, fwdEndMsg:
		m.forwardMsg(msg)
	case selCandidatesMsg:
		m.filterMsg(msg)
	}
	return m, nil
}

// globalKey handles the scope menus (c, n, f) and the section toggles
// (p, s, g), which work on every screen.
func (m *model) globalKey(key string) (tea.Cmd, bool) {
	switch key {
	case "c":
		if m.screen != screenContexts {
			m.leave()
			return m.openContexts(), true
		}
	case "n":
		if m.screen != screenNamespaces {
			m.leave()
			return m.openNamespaces(), true
		}
	case "f":
		if m.screen != screenFilter {
			m.leave()
			return m.openFilter(), true
		}
	case "p", "s", "g":
		for _, s := range sections {
			if s[:1] == key {
				m.leave()
				m.toggleSection(s)
				m.screen = screenMain
			}
		}
	default:
		return nil, false
	}
	return nil, true
}

// leave stops what the current sub screen streams before switching away.
func (m *model) leave() {
	switch m.screen {
	case screenLogs:
		if m.logs.cancel != nil {
			m.logs.cancel()
		}
		m.logs.gen++
	case screenNamespaces:
		m.namespaces.gen++ // stops its refresh
	case screenFilter:
		m.filter.gen++
	case screenSecret:
		m.secrets.data, m.secrets.openKey, m.secrets.reveal = nil, "", false
	}
}

// scopeChanged restarts the namespaced watches after the namespace or
// filter changed (gitops is cluster-wide and keeps running).
func (m *model) scopeChanged() {
	m.startPods()
	m.startSecrets()
	m.main.changesBack, m.main.offset = 0, 0
}

func (m *model) View() string {
	if m.width == 0 {
		return "starting…"
	}
	switch m.screen {
	case screenContainers:
		return m.containersView()
	case screenLogs:
		return m.logsView()
	case screenNamespaces:
		return m.namespacesView()
	case screenContexts:
		return m.contextsView()
	case screenFilter:
		return m.filterView()
	case screenSecret:
		return m.secretView()
	case screenPorts:
		return m.portsView()
	}
	return m.mainView()
}

func (m *model) setNotice(s string) {
	m.notice, m.noticeAt = s, time.Now()
}

// footer is the navigation bar: the scope menus (highlighted while their
// picker is open), then the section toggles (highlighted while shown), then
// this screen's commands — or a recent notice such as an error. Each menu is
// its name with the key letter (always the first) in cyan.
func (m *model) footer(sub string) string {
	item := func(name string, on bool) string {
		st, key := stMenu, stMenu.Foreground(lipgloss.Color("6")).Bold(true)
		if on {
			st, key = stMenuActive, stMenuActive.Underline(true)
		}
		return st.Render(" ") + key.Render(name[:1]) + st.Render(name[1:]+" ") + " "
	}
	var b strings.Builder
	for _, sc := range scopes {
		b.WriteString(item(sc.name, m.screen == sc.screen))
	}
	b.WriteString(stDim.Render("│ "))
	for _, s := range sections {
		b.WriteString(item(s, m.shown(s)))
	}
	right := hints(sub)
	if m.notice != "" && time.Since(m.noticeAt) < 6*time.Second {
		right = stYellow.Render(m.notice)
	}
	return clip(b.String()+" "+right, m.width)
}

// hints renders a screen's commands: the part in [brackets] is the key and
// shows in cyan (ideally a letter of the word, as in "s[h]ell"), the rest dim.
func hints(s string) string {
	var b strings.Builder
	for {
		i, j := strings.IndexByte(s, '['), strings.IndexByte(s, ']')
		if i < 0 || j < i {
			b.WriteString(stDim.Render(s))
			return b.String()
		}
		b.WriteString(stDim.Render(s[:i]) + stCyan.Bold(true).Render(s[i+1:j]))
		s = s[j+1:]
	}
}

// crumbs renders the top line of the sub screens for context ctxName. Put
// short, important parts first: the line is clipped on narrow terminals.
func (m *model) crumbs(ctxName string, parts ...string) string {
	all := []string{stBadge.Render("k7s")}
	for _, p := range parts {
		if p != "" {
			all = append(all, p)
		}
	}
	all = append(all, "ctx "+stCyan.Render(ctxName))
	return clip(strings.Join(all, stDim.Render(" │ ")), m.width)
}

// ---- styles & table rendering ---------------------------------------------

var (
	stHeader     = lipgloss.NewStyle().Bold(true)
	stBadge      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("0")).Background(lipgloss.Color("6")).Padding(0, 1)
	stDim        = lipgloss.NewStyle().Faint(true)
	stGreen      = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	stYellow     = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	stAmber      = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	stMenu       = lipgloss.NewStyle().Foreground(lipgloss.Color("250")).Background(lipgloss.Color("236"))
	stMenuActive = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("0")).Background(lipgloss.Color("6"))
	stRed        = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	stMagenta    = lipgloss.NewStyle().Foreground(lipgloss.Color("5"))
	stCyan       = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	stFlash      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("0")).Background(lipgloss.Color("3"))
	stDeleted    = lipgloss.NewStyle().Faint(true).Strikethrough(true)
	selectedBG   = lipgloss.Color("237")
)

type cell struct {
	text   string
	style  lipgloss.Style
	middle bool // shorten as "prefix…suffix" instead of cutting the end
}

// renderCells lays out one table line; widths and right must match cells.
func renderCells(cells []cell, widths []int, right []bool, selected bool, width int) string {
	sep := "  "
	parts := make([]string, len(cells))
	for i, c := range cells {
		text := truncate(c.text, widths[i])
		if c.middle {
			text = truncateMiddle(c.text, widths[i])
		}
		st := c.style
		if selected {
			st = st.Background(selectedBG)
		}
		parts[i] = st.Render(pad(text, widths[i], right[i]))
	}
	if selected {
		sep = lipgloss.NewStyle().Background(selectedBG).Render(sep)
	}
	line := clip(strings.Join(parts, sep), width)
	if selected {
		if gap := width - lipgloss.Width(line); gap > 0 {
			line += lipgloss.NewStyle().Background(selectedBG).Render(strings.Repeat(" ", gap))
		}
	}
	return line
}

// table is a selectable table: the sections of the main page and the pickers.
type table struct {
	headers []string
	right   []bool
	rows    [][]cell
	flex    int // column that shrinks when the terminal is too narrow
}

// render writes the header plus exactly height-1 row lines.
func (t table) render(cursor int, offset *int, height, width int) string {
	header, lines := t.lines(cursor, width)
	var b strings.Builder
	b.WriteString(header)
	b.WriteByte('\n')
	rows := max(1, height-1)
	*offset = scrollOffset(cursor, *offset, rows, len(lines))
	for i := 0; i < rows; i++ {
		if idx := *offset + i; idx < len(lines) {
			b.WriteString(lines[idx])
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// lines renders the header and every row (row cursor highlighted).
func (t table) lines(cursor, width int) (string, []string) {
	widths := make([]int, len(t.headers))
	for i, h := range t.headers {
		widths[i] = lipgloss.Width(h)
	}
	for _, r := range t.rows {
		for i, c := range r {
			widths[i] = max(widths[i], lipgloss.Width(c.text))
		}
	}
	total := -2
	for _, w := range widths {
		total += w + 2
	}
	if over := total - width; over > 0 {
		widths[t.flex] = max(8, widths[t.flex]-over)
	}

	hdr := make([]cell, len(t.headers))
	for i, h := range t.headers {
		hdr[i] = cell{text: h, style: stHeader}
	}
	lines := make([]string, len(t.rows))
	for i, r := range t.rows {
		lines[i] = renderCells(r, widths, t.right, i == cursor, width)
	}
	return renderCells(hdr, widths, t.right, false, width), lines
}

func scrollOffset(cursor, offset, rows, n int) int {
	if cursor < offset {
		offset = cursor
	}
	if cursor >= offset+rows {
		offset = cursor - rows + 1
	}
	return max(0, min(offset, max(0, n-rows)))
}

func clampCursor(cursor, delta, n int) int {
	if n == 0 {
		return 0
	}
	return max(0, min(n-1, cursor+delta))
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

// truncate shortens plain (unstyled) text to w cells, ending in "…".
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

// truncateMiddle shortens plain text to w cells as "prefix…suffix", keeping
// e.g. the replicaset/pod hash that tells replicas apart.
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
func clip(s string, w int) string {
	return lipgloss.NewStyle().MaxWidth(w).Render(s)
}

func orNone(s string) string {
	if s == "" {
		return "<none>"
	}
	return s
}

func dash(ok bool, s string) string {
	if !ok {
		return "-"
	}
	return s
}
