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

// The overview stacks the views of one namespace — the gitops apps that
// deploy what is listed, then pods, then secrets — on one page. Each section
// is the very same table as on its own screen (same data, same rows), with
// one merged change log underneath. It never spans all namespaces. A new
// view gets a section here.

type overviewState struct {
	selected    string // "<section>/<key>", e.g. "pods/<ns/name>"
	offset      int    // first visible body line
	changesBack int
}

// openOverview shows the overview of the current namespace and filter.
func (m *model) openOverview() tea.Cmd {
	m.screen, m.lastView = screenOverview, screenOverview
	if m.info.AllNamespaces {
		m.closeOverview() // the view explains that it needs one namespace
		return nil
	}
	m.startSecrets()
	return m.startGitOps()
}

func (m *model) closeOverview() {
	m.closeSecrets()
	m.closeGitOps()
}

// overviewApps lists the gitops apps responsible for what the overview
// shows: those deploying a listed secret or a listed pod's workload. With
// no filter, also every app deploying into the namespace.
func (m *model) overviewApps() []string {
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

// overviewEntries lists the selectable rows in display order.
func (m *model) overviewEntries() []string {
	var out []string
	for _, k := range m.overviewApps() {
		out = append(out, "gitops/"+k)
	}
	for _, k := range m.pods.order {
		out = append(out, "pods/"+k)
	}
	for _, k := range m.secrets.order {
		out = append(out, "secrets/"+k)
	}
	return out
}

func (m *model) overviewMove(delta int) {
	entries := m.overviewEntries()
	if len(entries) == 0 {
		return
	}
	i := max(0, slices.Index(entries, m.overview.selected))
	m.overview.selected = entries[clampCursor(i, delta, len(entries))]
}

// overviewSelection returns the selected pod or secret.
func (m *model) overviewSelection() (*pods.Row, *secrets.Row) {
	kind, key, _ := strings.Cut(m.overview.selected, "/")
	switch kind {
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

func (m *model) overviewKey(msg tea.KeyMsg) tea.Cmd {
	page := m.podsLayout().tableRows
	pod, sec := m.overviewSelection()
	switch key := msg.String(); key {
	case "esc":
		m.closeOverview()
		m.screen, m.lastView = screenPods, screenPods
	case "up", "k":
		m.overviewMove(-1)
	case "down", "j":
		m.overviewMove(1)
	case "pgup":
		m.overviewMove(-page)
	case "pgdown":
		m.overviewMove(page)
	case "home":
		m.overviewMove(-len(m.overviewEntries()))
	case "end", "G":
		m.overviewMove(len(m.overviewEntries()))
	case "q":
		m.overview.changesBack++ // the pane clamps it
	case "a":
		m.overview.changesBack = max(0, m.overview.changesBack-1)
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

func (m *model) overviewStatusLine(appKeys []string) string {
	parts := []string{
		stBadge.Render("k7s"),
		"ctx " + stCyan.Render(m.info.Context),
		"ns " + stCyan.Render(m.nsLabel()),
		m.filterLabel(),
		fmt.Sprintf("pods %d · secrets %d", len(m.pods.order), len(m.secrets.order)),
	}
	// One light for the whole deployment chain: an all-green pod list under
	// a broken app (or a broken parent app) must not look fine.
	if len(appKeys) > 0 {
		parts = append(parts, "gitops "+lightStyle(m.gitopsLight(appKeys)).Render("●")+
			fmt.Sprintf(" %d apps", len(appKeys)))
	}
	switch {
	case m.info.AllNamespaces:
	case m.pods.synced && m.secrets.synced:
		parts = append(parts, m.pulse()+" "+stCyan.Render("watching"))
	default:
		parts = append(parts, stYellow.Render("○ syncing"))
	}
	return clip(strings.Join(parts, stDim.Render(" │ ")), m.width)
}

// filterLabel describes the filter for the status lines.
func (m *model) filterLabel() string {
	if m.info.Selector == "" {
		return "filter " + stDim.Render("none")
	}
	return "filter " + stCyan.Render(m.info.Selector)
}

func (m *model) overviewView() string {
	o := &m.overview
	l := m.podsLayout()
	appKeys := m.overviewApps()
	var b strings.Builder
	b.WriteString(m.overviewStatusLine(appKeys))
	b.WriteString("\n\n")

	if m.info.AllNamespaces {
		b.WriteString(stAmber.Render("  the overview shows one namespace: pick one with n, or press N to leave all namespaces"))
		b.WriteString(strings.Repeat("\n", m.height-3))
		return b.String() + m.footer("[esc] back")
	}

	entries := m.overviewEntries()
	if !slices.Contains(entries, o.selected) && len(entries) > 0 {
		o.selected = entries[0]
	}
	kind, key, _ := strings.Cut(o.selected, "/")

	// Build every section's lines, remembering where the selected row is.
	var lines []string
	selLine := 0
	section := func(title string, n int) {
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, stHeader.Render(fmt.Sprintf("%s (%d)", title, n)))
	}

	// GitOps first: it is what deploys everything below.
	if len(appKeys) > 0 {
		section("gitops", len(appKeys))
		cursor := -1
		if kind == "gitops" {
			cursor = slices.Index(appKeys, key)
		}
		header, rows := m.gitopsTable(appKeys).lines(cursor, m.width)
		lines = append(lines, header)
		if cursor >= 0 {
			selLine = len(lines) + cursor
		}
		lines = append(lines, rows...)
	}

	section("pods", len(m.pods.order))
	if len(m.pods.order) == 0 {
		lines = append(lines, stDim.Render(m.emptyMessage()))
	} else {
		sel := ""
		if kind == "pods" {
			sel = key
		}
		header, rows := m.podLines(sel)
		lines = append(lines, header)
		for i, r := range rows {
			if sel != "" && m.pods.order[i] == sel {
				selLine = len(lines)
			}
			lines = append(lines, r)
		}
	}

	section("secrets", len(m.secrets.order))
	if len(m.secrets.order) == 0 {
		empty := "  no secrets"
		if !m.secrets.synced {
			empty = "  waiting for secrets…"
		}
		lines = append(lines, stDim.Render(empty))
	} else {
		cursor := -1
		if kind == "secrets" {
			cursor = slices.Index(m.secrets.order, key)
		}
		header, rows := m.secretsTable().lines(cursor, m.width)
		lines = append(lines, header)
		if cursor >= 0 {
			selLine = len(lines) + cursor
		}
		lines = append(lines, rows...)
	}

	bodyH := l.tableRows + 1
	o.offset = scrollOffset(selLine, o.offset, bodyH, len(lines))
	for i := 0; i < bodyH; i++ {
		if idx := o.offset + i; idx < len(lines) {
			b.WriteString(lines[idx])
		}
		b.WriteByte('\n')
	}

	changes := m.overviewChanges(appKeys)
	b.WriteString(m.changesPane(l.eventLines, len(changes), &o.changesBack,
		func(i int) string { return changes[i].line() },
		"no changes yet — pod and app changes appear here"))
	cmds := "[q/a] changes  [esc] back"
	switch pod, sec := m.overviewSelection(); {
	case pod != nil:
		cmds = "[l]ogs  s[h]ell  for[w]ard  " + cmds
	case sec != nil:
		cmds = "[enter] open  " + cmds
	}
	b.WriteString(m.footer(cmds))
	return b.String()
}

type change struct {
	at   time.Time
	line func() string
}

// overviewChanges merges the pod change log with the changes of the
// responsible apps, oldest first.
func (m *model) overviewChanges(appKeys []string) []change {
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
