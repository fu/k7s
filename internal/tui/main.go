package tui

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/fu/k7s/internal/gitops"
	"github.com/fu/k7s/internal/pods"
	"github.com/fu/k7s/internal/secrets"
)

// The main page is all of k7s: one namespace (and filter), its resources
// stacked in sections — the gitops apps deploying them, pods, secrets — and
// one change log underneath. Section keys (p, s, g) show or hide sections.

// sections in display order; each is also a menu key (its first letter).
var sections = []string{"gitops", "pods", "secrets"}

type mainState struct {
	hidden      map[string]bool
	selected    string // "<section>/<key>", e.g. "pods/<ns/name>"
	offset      int    // first visible body line
	changesBack int
	jumpBuf     string
	jumpAt      time.Time
}

func (m *model) shown(section string) bool { return !m.main.hidden[section] }

func (m *model) toggleSection(section string) {
	if m.main.hidden == nil {
		m.main.hidden = map[string]bool{}
	}
	m.main.hidden[section] = !m.main.hidden[section]
}

// responsibleApps lists the gitops apps responsible for what is shown:
// those deploying a listed secret or a listed pod's workload. With no
// filter, also every app deploying into the namespace.
func (m *model) responsibleApps() []string {
	g := &m.gitops
	if !g.detected || g.engine == gitops.None {
		return nil
	}
	apps, _ := g.apps()
	listed := map[gitops.Ref]bool{}
	for _, k := range m.pods.order {
		if r := m.pods.rows[k].row; r.WorkloadKind != "" {
			listed[gitops.Ref{Kind: r.WorkloadKind, Namespace: r.Namespace, Name: r.WorkloadName}] = true
		}
	}
	for _, k := range m.secrets.order {
		r := m.secrets.rows[k]
		listed[gitops.Ref{Kind: "Secret", Namespace: r.Namespace, Name: r.Name}] = true
	}
	var keys []string
	for k, a := range apps {
		relevant := m.info.Selector == "" && a.DestNS == m.info.Namespace
		for _, ref := range a.Manages {
			if listed[ref] || (m.info.Selector == "" && ref.Namespace == m.info.Namespace) {
				relevant = true
				break
			}
		}
		if relevant {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
}

// gitopsLight is the worst light of the responsible apps and their parents.
func (m *model) gitopsLight(keys []string) gitops.Light {
	apps, parents := m.gitops.apps()
	worst := gitops.Grey
	for _, k := range keys {
		worst = max(worst, apps[k].Overall(), gitops.ChainLight(k, apps, parents))
	}
	return worst
}

// sectionKeys lists a section's rows (keys without the section prefix).
func (m *model) sectionKeys(section string, appKeys []string) []string {
	switch section {
	case "gitops":
		return appKeys
	case "pods":
		return m.pods.order
	case "secrets":
		return m.secrets.order
	}
	return nil
}

// entries lists the selectable rows of the shown sections, in order.
func (m *model) entries(appKeys []string) []string {
	var out []string
	for _, s := range sections {
		if m.shown(s) {
			for _, k := range m.sectionKeys(s, appKeys) {
				out = append(out, s+"/"+k)
			}
		}
	}
	return out
}

func (m *model) moveSelection(delta int) {
	entries := m.entries(m.responsibleApps())
	if len(entries) == 0 {
		return
	}
	i := max(0, slices.Index(entries, m.main.selected))
	m.main.selected = entries[clampCursor(i, delta, len(entries))]
}

// selection returns the selected pod or secret.
func (m *model) selection() (*pods.Row, *secrets.Row) {
	section, key, _ := strings.Cut(m.main.selected, "/")
	switch section {
	case "pods":
		if rs, ok := m.pods.rows[key]; ok {
			return &rs.row, nil
		}
	case "secrets":
		if r, ok := m.secrets.rows[key]; ok {
			return nil, &r
		}
	}
	return nil, nil
}

func (m *model) mainKey(msg tea.KeyMsg) tea.Cmd {
	page := m.mainLayout().body
	key := msg.String()
	if isDigit(key) {
		// Jump to row #n of the section the cursor is in.
		section, _, _ := strings.Cut(m.main.selected, "/")
		keys := m.sectionKeys(section, m.responsibleApps())
		if n := typeNumber(&m.main.jumpBuf, &m.main.jumpAt, key); n >= 1 && n <= len(keys) {
			m.main.selected = section + "/" + keys[n-1]
		}
		return nil
	}
	pod, sec := m.selection()
	switch key {
	case "esc":
		return tea.Quit
	case "up", "k":
		m.moveSelection(-1)
	case "down", "j":
		m.moveSelection(1)
	case "pgup":
		m.moveSelection(-page)
	case "pgdown":
		m.moveSelection(page)
	case "home":
		m.moveSelection(-1 << 30)
	case "end", "G":
		m.moveSelection(1 << 30)
	case "q":
		m.main.changesBack++ // the pane clamps it
	case "a":
		m.main.changesBack = max(0, m.main.changesBack-1)
	case "l":
		if pod != nil {
			return m.openContainersFor(*pod, forLogs)
		}
	case "h":
		if pod != nil {
			return m.openContainersFor(*pod, forShell)
		}
	case "w":
		if pod != nil {
			return m.toggleForwardFor(*pod)
		}
	case "enter":
		switch {
		case pod != nil:
			return m.openContainersFor(*pod, forLogs)
		case sec != nil:
			return m.openSecret(*sec)
		}
	}
	return nil
}

// ---- rendering -----------------------------------------------------------

type mainLayout struct {
	body       int // lines for the sections
	eventLines int
}

func (m *model) mainLayout() mainLayout {
	l := mainLayout{eventLines: max(4, min(12, m.height/3))}
	// status line + blank + body + changes rule + changes + footer
	l.body = max(1, m.height-2-1-l.eventLines-1)
	return l
}

// filterLabel describes the filter for the status line.
func (m *model) filterLabel() string {
	if m.info.Selector == "" {
		return "filter " + stDim.Render("none")
	}
	return "filter " + stCyan.Render(m.info.Selector)
}

func (m *model) mainStatusLine(appKeys []string) string {
	healthy, progressing, failing := m.podCounts()
	parts := []string{
		stBadge.Render("k7s"),
		"ctx " + stCyan.Render(m.info.Context),
		"ns " + stCyan.Render(m.info.Namespace),
		m.filterLabel(),
		fmt.Sprintf("pods %d %s %s %s", len(m.pods.order),
			stGreen.Render(fmt.Sprintf("●%d", healthy)),
			stAmber.Render(fmt.Sprintf("●%d", progressing)),
			stRed.Render(fmt.Sprintf("●%d", failing))),
		fmt.Sprintf("secrets %d", len(m.secrets.order)),
	}
	// One light for the whole deployment chain: an all-green pod list under
	// a broken app (or a broken parent app) must not look fine.
	if len(appKeys) > 0 {
		parts = append(parts, "gitops "+lightStyle(m.gitopsLight(appKeys)).Render("●")+fmt.Sprintf(" %d apps", len(appKeys)))
	}
	if n := len(m.forwards); n > 0 {
		parts = append(parts, stCyan.Render(fmt.Sprintf("⇄ %d forwarded", n)))
	}
	recent := func(err error, at time.Time) bool { return err != nil && time.Since(at) < 30*time.Second }
	switch {
	case recent(m.pods.watchErr, m.pods.watchErrAt):
		parts = append(parts, stRed.Render("watch error: "+m.pods.watchErr.Error()))
	case recent(m.secrets.err, m.secrets.errAt):
		parts = append(parts, stRed.Render("secrets: "+m.secrets.err.Error()))
	case recent(m.gitops.err, m.gitops.errAt):
		parts = append(parts, stRed.Render("gitops: "+m.gitops.err.Error()))
	case m.pods.synced && m.secrets.synced:
		parts = append(parts, m.pulse()+" "+stCyan.Render("watching"))
	default:
		parts = append(parts, stYellow.Render("○ syncing"))
	}
	if m.pods.metricsErr != nil {
		parts = append(parts, stRed.Render("metrics unavailable"))
	}
	return clip(strings.Join(parts, stDim.Render(" │ ")), m.width)
}

func (m *model) mainView() string {
	o := &m.main
	l := m.mainLayout()
	appKeys := m.responsibleApps()
	var b strings.Builder
	b.WriteString(m.mainStatusLine(appKeys))
	b.WriteString("\n\n")

	entries := m.entries(appKeys)
	if !slices.Contains(entries, o.selected) && len(entries) > 0 {
		o.selected = entries[0]
	}
	selSection, selKey, _ := strings.Cut(o.selected, "/")

	// Build every shown section's lines, remembering where the selected row is.
	var lines []string
	selLine := 0
	title := func(name string, n int) {
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, stHeader.Render(fmt.Sprintf("%s (%d)", name, n)))
	}
	addTable := func(section string, header string, rows []string, cursor int) {
		lines = append(lines, header)
		if section == selSection && cursor >= 0 {
			selLine = len(lines) + cursor
		}
		lines = append(lines, rows...)
	}
	cursorOf := func(section string, keys []string) int {
		if section != selSection {
			return -1
		}
		return slices.Index(keys, selKey)
	}

	for _, s := range sections {
		if !m.shown(s) {
			continue
		}
		switch s {
		case "gitops":
			// Only when the cluster has GitOps; it is what deploys the rest.
			if !m.gitops.detected || m.gitops.engine == gitops.None {
				continue
			}
			title("gitops", len(appKeys))
			if len(appKeys) == 0 {
				lines = append(lines, stDim.Render("  no app deploys what is listed here"))
				continue
			}
			c := cursorOf(s, appKeys)
			header, rows := m.gitopsTable(appKeys).lines(c, m.width)
			addTable(s, header, rows, c)
		case "pods":
			title("pods", len(m.pods.order))
			if len(m.pods.order) == 0 {
				lines = append(lines, stDim.Render(m.emptyMessage()))
				continue
			}
			sel := ""
			if selSection == "pods" {
				sel = selKey
			}
			header, rows := m.podLines(sel)
			addTable(s, header, rows, cursorOf(s, m.pods.order))
		case "secrets":
			title("secrets", len(m.secrets.order))
			if len(m.secrets.order) == 0 {
				empty := "  no secrets"
				if !m.secrets.synced {
					empty = "  waiting for secrets…"
				}
				lines = append(lines, stDim.Render(empty))
				continue
			}
			c := cursorOf(s, m.secrets.order)
			header, rows := m.secretsTable().lines(c, m.width)
			addTable(s, header, rows, c)
		}
	}
	if len(lines) == 0 {
		lines = append(lines, stDim.Render("  all sections hidden: press p, s or g to show them"))
	}

	o.offset = scrollOffset(selLine, o.offset, l.body, len(lines))
	for i := 0; i < l.body; i++ {
		if idx := o.offset + i; idx < len(lines) {
			b.WriteString(lines[idx])
		}
		b.WriteByte('\n')
	}

	changes := m.mainChanges(appKeys)
	b.WriteString(m.changesPane(l.eventLines, len(changes), &o.changesBack,
		func(i int) string { return changes[i].line() },
		"no changes yet — pods, secrets, apps and Kubernetes events of this namespace appear here"))

	cmds := "[q/a] changes  [esc] quit"
	switch pod, sec := m.selection(); {
	case pod != nil:
		cmds = "[l]ogs  s[h]ell  for[w]ard  " + cmds
	case sec != nil:
		cmds = "[enter] open  " + cmds
	}
	b.WriteString(m.footer(jumpOr(o.jumpBuf, o.jumpAt, cmds)))
	return b.String()
}

type change struct {
	at   time.Time
	line func() string
}

// mainChanges merges the namespace's change log (pods, secrets, Kubernetes
// events) with the changes of the responsible apps, oldest first.
func (m *model) mainChanges(appKeys []string) []change {
	var out []change
	for _, e := range m.pods.events {
		out = append(out, change{e.at, func() string { return m.eventLineView(e) }})
	}
	for _, e := range m.gitops.events {
		if slices.Contains(appKeys, e.app.Key()) {
			out = append(out, change{e.at, func() string { return m.appEventView(e) }})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].at.Before(out[j].at) })
	return out
}
