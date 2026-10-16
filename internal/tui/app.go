// Package tui renders k7s's live views. Top-level menus (pods, namespaces,
// contexts, gitops, secrets) are global keys shown in the navigation bar;
// each screen adds its own commands to the right. Esc goes back, and quits
// on the pods screen.
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
	Context       string
	Server        string
	Namespace     string
	AllNamespaces bool
	Selector      string
	Refresh       time.Duration
	Start         string // view to open first (a menu name)
	Notice        string // shown in the navigation bar at start
}

// ViewNames lists the top-level views in menu order.
func ViewNames() []string {
	names := make([]string, len(menus))
	for i, mu := range menus {
		names[i] = mu.name
	}
	return names
}

// viewAliases accepts kubectl-style singular and short names.
var viewAliases = map[string]string{
	"pod": "pods", "po": "pods",
	"secret":    "secrets",
	"namespace": "namespaces", "ns": "namespaces",
	"context": "contexts", "ctx": "contexts",
	"all":  "overview",
	"argo": "gitops", "argocd": "gitops", "flux": "gitops",
	"application": "gitops", "applications": "gitops", "app": "gitops",
	"kustomization": "gitops", "kustomizations": "gitops", "helmrelease": "gitops", "helmreleases": "gitops",
}

// ResolveView maps a command-line argument to a view name.
func ResolveView(arg string) (string, bool) {
	arg = strings.ToLower(arg)
	if a, ok := viewAliases[arg]; ok {
		arg = a
	}
	for _, mu := range menus {
		if mu.name == arg {
			return arg, true
		}
	}
	return "", false
}

type screen int

const (
	screenPods screen = iota
	screenContainers
	screenLogs
	screenNamespaces
	screenContexts
	screenGitOps
	screenSecrets
	screenSecret // one secret's keys
	screenPorts  // pick a port to forward
	screenOverview
	screenFilter // pick the label selector (a scope, like namespace)
)

// menus are the top-level views, always shown in the navigation bar. The
// first letter of each name is its key.
var menus = []struct {
	name   string
	screen screen
}{
	{"contexts", screenContexts},
	{"namespaces", screenNamespaces},
	{"filter", screenFilter},
	{"overview", screenOverview},
	{"pods", screenPods},
	{"secrets", screenSecrets},
	{"gitops", screenGitOps},
}

type tickMsg time.Time

type model struct {
	ctx      context.Context
	startCmd tea.Cmd                        // opens the start view
	flags    *genericclioptions.ConfigFlags // to build clients for other contexts
	client   *kube.Client                   // the context in use
	send     func(tea.Msg)
	info     Info

	screen        screen
	lastView      screen // the last menu view: where n/c return after a switch
	width, height int
	notice        string
	noticeAt      time.Time

	pods       podsState
	containers containersState
	logs       logsState
	namespaces nsState
	contexts   ctxState
	gitops     gitopsState
	secrets    secretsState
	ports      portsState
	overview   overviewState
	filter     filterState
	forwards   map[string]*forward // active port-forwards by pod/port
}

// Run starts the live pod view and blocks until the user quits.
func Run(ctx context.Context, flags *genericclioptions.ConfigFlags, client *kube.Client, info Info) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	m := &model{ctx: ctx, flags: flags, client: client, info: info, forwards: map[string]*forward{}}
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithContext(ctx))
	m.send = p.Send
	m.startPods()
	switch info.Start {
	case "secrets":
		m.startCmd = m.openSecrets()
	case "gitops":
		m.startCmd = m.openGitOps()
	case "namespaces":
		m.startCmd = m.openNamespaces()
	case "contexts":
		m.startCmd = m.openContexts()
	case "overview":
		m.startCmd = m.openOverview()
	}
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
		case screenGitOps:
			return m, m.gitopsKey(msg)
		case screenSecrets:
			return m, m.secretsKey(msg)
		case screenSecret:
			return m, m.secretKey(msg)
		case screenPorts:
			return m, m.portsKey(msg)
		case screenOverview:
			return m, m.overviewKey(msg)
		case screenFilter:
			return m, m.filterKey(msg)
		default:
			return m, m.podsKey(msg)
		}
	case tickMsg:
		m.pods.purgeDeleted()
		if m.screen == screenGitOps {
			m.gitops.resort() // drops deleted apps after a while
		}
		return m, tick()
	case gitopsDetectedMsg, appMsg, appsSyncedMsg, appsErrMsg:
		m.gitopsMsg(msg)
	case pulseMsg:
		return m, pulseTick()
	case shellDoneMsg:
		if msg.err != nil {
			m.setNotice("shell: " + msg.err.Error())
		}
	case podMsg, metricsMsg, syncedMsg, watchErrMsg, warningMsg:
		m.podsMsg(msg)
	case logLineMsg, logEndMsg:
		m.logsMsg(msg)
	case nsLoadedMsg, nsRefreshMsg:
		return m, m.namespacesMsg(msg)
	case secretMsg, secretsSyncedMsg, secretsErrMsg, secretDataMsg:
		m.secretsMsg(msg)
	case fwdReadyMsg, fwdEndMsg:
		m.forwardMsg(msg)
	case selCandidatesMsg:
		m.filterMsg(msg)
	}
	return m, nil
}

// globalKey handles the menu keys and N, which work on every screen.
func (m *model) globalKey(key string) (tea.Cmd, bool) {
	switch key {
	case "p":
		m.leave()
		m.screen, m.lastView = screenPods, screenPods
	case "n":
		if m.screen != screenNamespaces {
			m.leave()
			return m.openNamespaces(), true
		}
	case "c":
		if m.screen != screenContexts {
			m.leave()
			return m.openContexts(), true
		}
	case "g":
		if m.screen != screenGitOps {
			m.leave()
			return m.openGitOps(), true
		}
	case "s":
		if m.screen != screenSecrets {
			m.leave()
			return m.openSecrets(), true
		}
	case "o":
		if m.screen != screenOverview {
			m.leave()
			return m.openOverview(), true
		}
	case "f":
		if m.screen != screenFilter {
			m.leave()
			return m.openFilter(), true
		}
	case "N":
		if m.lastView == screenOverview && !m.info.AllNamespaces {
			m.setNotice("the overview shows one namespace; N works in the other views")
			return nil, true
		}
		m.info.AllNamespaces = !m.info.AllNamespaces
		if m.info.AllNamespaces {
			m.setNotice("all namespaces")
		} else {
			m.setNotice("namespace " + m.info.Namespace)
		}
		return m.scopeChanged(), true
	default:
		return nil, false
	}
	return nil, true
}

// leave stops whatever the current screen streams before switching away.
func (m *model) leave() {
	switch m.screen {
	case screenLogs:
		if m.logs.cancel != nil {
			m.logs.cancel()
		}
		m.logs.gen++
	case screenGitOps:
		m.closeGitOps()
	case screenSecrets, screenSecret:
		m.closeSecrets()
	case screenNamespaces:
		m.namespaces.gen++ // stops its refresh
	case screenFilter:
		m.filter.gen++
	}
	// The overview keeps watching while its logs/secret/pickers are open.
	if m.lastView == screenOverview {
		m.closeOverview()
	}
}

// scopeChanged restarts the watches after the context or namespace changed.
func (m *model) scopeChanged() tea.Cmd {
	m.startPods()
	switch m.screen {
	case screenSecrets, screenSecret:
		return m.openSecrets()
	case screenOverview:
		return m.openOverview()
	case screenFilter:
		return m.openFilter()
	}
	return nil // gitops is cluster-wide: the namespace does not matter
}

// returnToView goes back to the last resource view after a switch.
func (m *model) returnToView() tea.Cmd {
	switch m.lastView {
	case screenGitOps:
		return m.openGitOps()
	case screenSecrets:
		return m.openSecrets()
	case screenOverview:
		return m.openOverview()
	}
	m.screen = screenPods
	return nil
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
	case screenGitOps:
		return m.gitopsView()
	case screenSecrets:
		return m.secretsView()
	case screenSecret:
		return m.secretView()
	case screenPorts:
		return m.portsView()
	case screenOverview:
		return m.overviewView()
	case screenFilter:
		return m.filterView()
	}
	return m.podsView()
}

func (m *model) setNotice(s string) {
	m.notice, m.noticeAt = s, time.Now()
}

// footer is the navigation bar: the global menus on a discreet background
// (the active one highlighted), then this screen's commands — or a recent
// notice such as an error.
func (m *model) footer(sub string) string {
	// Sub screens (logs, pickers, an opened secret) belong to the menu view
	// they were opened from.
	active := m.screen
	switch active {
	case screenContainers, screenLogs, screenPorts, screenSecret:
		active = m.lastView
	}
	// Each menu is its name with the key letter (always the first) in cyan.
	var b strings.Builder
	for _, mu := range menus {
		st, key := stMenu, stMenu.Foreground(lipgloss.Color("6")).Bold(true)
		if mu.screen == active {
			st, key = stMenuActive, stMenuActive.Underline(true)
		}
		b.WriteString(st.Render(" ") + key.Render(mu.name[:1]) + st.Render(mu.name[1:]+" ") + " ")
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

// scopeHint describes what N switches to.
func (m *model) scopeHint() string {
	if m.info.AllNamespaces {
		return "[N]amespace " + m.info.Namespace
	}
	return "all [N]amespaces"
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

// table is a simple selectable list used by the container and namespace screens.
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

// lines renders the header and every row (row cursor highlighted). Views
// share it so the overview can stack the very same rows.
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
