package tui

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/fu/k7s/internal/gitops"
	"github.com/fu/k7s/internal/pods"
)

// The GitOps screen sits next to pods under context › namespace. GitOps is
// cluster-wide (Argo CD keeps its Applications in its own namespace), so it
// lists every app and highlights the ones deploying into the namespace you
// came from.

type (
	gitopsDetectedMsg struct {
		gen    int
		engine gitops.Engine
	}
	appMsg struct {
		gen int
		ev  gitops.Event
	}
	appsSyncedMsg struct {
		gen int
	}
	appsErrMsg struct {
		gen int
		err error
	}
)

type appRow struct {
	app           gitops.App
	syncChanged   time.Time
	healthChanged time.Time
	deletedAt     time.Time
}

type appEvent struct {
	at     time.Time
	kind   string
	app    gitops.App
	detail string
	items  []string // one line each, e.g. every degraded / out-of-sync resource
}

type gitopsState struct {
	gen      int
	cancel   context.CancelFunc
	detected bool
	engine   gitops.Engine

	rows           map[string]*appRow
	order          []string
	selected       string
	cursor, offset int
	jumpBuf        string
	jumpAt         time.Time

	events      []appEvent
	changesBack int
	synced      bool
	err         error
	errAt       time.Time
}

// openGitOps detects the engine of the current context and starts watching.
func (m *model) openGitOps() tea.Cmd {
	g := &m.gitops
	if g.cancel != nil {
		g.cancel()
	}
	gen := g.gen + 1
	*g = gitopsState{gen: gen, rows: map[string]*appRow{}}
	m.screen, m.lastView = screenGitOps, screenGitOps
	client := m.client
	return func() tea.Msg { return gitopsDetectedMsg{gen, gitops.Detect(client)} }
}

func (m *model) closeGitOps() {
	g := &m.gitops
	if g.cancel != nil {
		g.cancel()
	}
	g.gen++
	m.screen = screenPods
}

func (m *model) gitopsMsg(msg tea.Msg) {
	g := &m.gitops
	switch msg := msg.(type) {
	case gitopsDetectedMsg:
		if msg.gen != g.gen {
			return
		}
		g.detected, g.engine = true, msg.engine
		if msg.engine == gitops.None {
			return
		}
		ctx, cancel := context.WithCancel(m.ctx)
		g.cancel = cancel
		gen, send := g.gen, m.send
		err := gitops.Watch(ctx, m.client, msg.engine, gitops.Handlers{
			App:    func(e gitops.Event) { send(appMsg{gen, e}) },
			Synced: func() { send(appsSyncedMsg{gen}) },
			Error:  func(err error) { send(appsErrMsg{gen, err}) },
		})
		if err != nil {
			g.err, g.errAt = err, time.Now()
		}
	case appMsg:
		if msg.gen == g.gen {
			g.apply(msg.ev)
		}
	case appsSyncedMsg:
		if msg.gen == g.gen && !g.synced {
			g.synced = true
			g.logInitialProblems()
		}
	case appsErrMsg:
		if msg.gen == g.gen {
			g.err, g.errAt = msg.err, time.Now()
		}
	}
}

func (g *gitopsState) apply(e gitops.Event) {
	now := time.Now()
	key := e.App.Key()
	row, exists := g.rows[key]
	switch {
	case e.Type == gitops.Deleted:
		if exists {
			row.app, row.deletedAt = e.App, now
		}
		g.log(appEvent{at: now, kind: "DELETED", app: e.App})
	case !exists || !row.deletedAt.IsZero():
		g.rows[key] = &appRow{app: e.App}
		if !e.Initial {
			g.log(appEvent{at: now, kind: "ADDED", app: e.App})
		}
	default:
		old := row.app
		var details []string
		if old.Sync != e.App.Sync {
			row.syncChanged = now
			details = append(details, fmt.Sprintf("sync %s → %s", old.Sync, e.App.Sync))
		}
		if old.Health != e.App.Health {
			row.healthChanged = now
			details = append(details, fmt.Sprintf("health %s → %s", old.Health, e.App.Health))
		}
		if old.Revision != e.App.Revision && e.App.Revision != "" {
			details = append(details, fmt.Sprintf("revision %s → %s", orNone(old.Revision), e.App.Revision))
		}
		if old.OpPhase != e.App.OpPhase && e.App.OpPhase != "" {
			op := "sync " + strings.ToLower(e.App.OpPhase)
			if e.App.OpPhase != "Succeeded" && e.App.OpMessage != "" {
				op += ": " + e.App.OpMessage
			}
			details = append(details, op)
		}
		// When it turns red, say why and list every problem resource.
		var items []string
		if e.App.Overall() == gitops.Red && old.Overall() != gitops.Red {
			details = append(details, e.App.Explain())
			items = e.App.Problems
		}
		row.app = e.App
		if len(details) > 0 {
			g.log(appEvent{at: now, kind: "MODIFIED", app: e.App, detail: strings.Join(slices.DeleteFunc(details, func(s string) bool { return s == "" }), ", "), items: items})
		}
	}
	g.resort()
}

// logInitialProblems explains every app that is not green when the watch starts.
func (g *gitopsState) logInitialProblems() {
	now := time.Now()
	for _, k := range g.order {
		if a := g.rows[k].app; a.Overall() != gitops.Green {
			g.log(appEvent{at: now, kind: string(stateKind), app: a, detail: a.Explain(), items: a.Problems})
		}
	}
}

func (g *gitopsState) log(e appEvent) {
	g.events = append(g.events, e)
	if len(g.events) > maxEvents {
		g.events = g.events[len(g.events)-maxEvents:]
	}
	if g.changesBack > 0 { // keep a scrolled-up pane looking at the same lines
		g.changesBack += 1 + len(e.items)
	}
}

func (g *gitopsState) resort() {
	g.order = g.order[:0]
	for k, row := range g.rows {
		if !row.deletedAt.IsZero() && time.Since(row.deletedAt) > keepDeleted {
			delete(g.rows, k)
			continue
		}
		g.order = append(g.order, k)
	}
	sort.Strings(g.order)
	g.cursor = 0
	for i, k := range g.order {
		if k == g.selected {
			g.cursor = i
		}
	}
	if len(g.order) > 0 {
		g.selected = g.order[g.cursor]
	}
}

func (g *gitopsState) move(delta int) {
	if len(g.order) == 0 {
		return
	}
	g.cursor = clampCursor(g.cursor, delta, len(g.order))
	g.selected = g.order[g.cursor]
}

func (m *model) gitopsKey(msg tea.KeyMsg) tea.Cmd {
	g := &m.gitops
	page := m.podsLayout().tableRows
	key := msg.String()
	if isDigit(key) {
		if n := typeNumber(&g.jumpBuf, &g.jumpAt, key); n >= 1 && n <= len(g.order) {
			g.cursor, g.selected = n-1, g.order[n-1]
		}
		return nil
	}
	switch key {
	case "esc":
		m.closeGitOps()
		m.lastView = screenPods
	case "up", "k":
		g.move(-1)
	case "down", "j":
		g.move(1)
	case "pgup":
		g.move(-page)
	case "pgdown":
		g.move(page)
	case "home":
		g.move(-len(g.order))
	case "end", "G":
		g.move(len(g.order))
	case "q":
		g.changesBack++ // the pane clamps it to what can be scrolled
	case "a":
		g.changesBack = max(0, g.changesBack-1)
	}
	return nil
}

// ---- rendering -----------------------------------------------------------

func lightStyle(l gitops.Light) lipgloss.Style {
	switch l {
	case gitops.Green:
		return stGreen
	case gitops.Amber:
		return stAmber
	case gitops.Red:
		return stRed.Bold(true)
	}
	return stDim
}

func (m *model) gitopsView() string {
	g := &m.gitops
	l := m.podsLayout()
	var b strings.Builder
	b.WriteString(m.gitopsStatusLine())
	b.WriteString("\n\n")

	switch {
	case !g.detected:
		b.WriteString(stDim.Render("  looking for Argo CD or Flux…"))
		b.WriteString(strings.Repeat("\n", m.height-3))
		return b.String() + m.footer("[esc] back")
	case g.engine == gitops.None:
		b.WriteString(stDim.Render("  no Argo CD or Flux found on this cluster"))
		b.WriteString(strings.Repeat("\n", m.height-3))
		return b.String() + m.footer("[esc] back")
	}

	// Flux mixes Kustomizations and HelmReleases, so it gets a KIND column.
	flux := g.engine == gitops.Flux
	t := table{
		headers: []string{"#", "SYNC", "HEALTH", "NAME", "DEST", "SOURCE", "REV", "SYNCED"},
		right:   []bool{true, false, false, false, false, false, false, false},
		flex:    5,
	}
	if flux {
		t.headers = slices.Insert(t.headers, 3, "KIND")
		t.right = slices.Insert(t.right, 3, false)
		t.flex = 6
	}
	now := time.Now()
	here := !m.info.AllNamespaces
	for i, k := range g.order {
		row := g.rows[k]
		a := row.app
		sync := cell{text: "●", style: lightStyle(a.SyncLight())}
		health := cell{text: "●", style: lightStyle(a.HealthLight())}
		if now.Sub(row.syncChanged) < flashFor {
			sync.style = stFlash
		}
		if now.Sub(row.healthChanged) < flashFor {
			health.style = stFlash
		}
		name, dest := cell{text: a.Name, middle: true}, cell{text: a.DestNS, style: stDim}
		if here && a.DestNS == m.info.Namespace {
			name.style, dest.style = stCyan.Bold(true), stCyan
		}
		synced := cell{text: "-", style: stDim}
		if !a.SyncedAt.IsZero() {
			synced = cell{text: pods.Age(a.SyncedAt), style: stDim}
		}
		cells := []cell{
			{text: fmt.Sprint(i + 1), style: stDim},
			sync, health, name, dest,
			{text: a.Source, style: stDim, middle: true},
			{text: a.Revision, style: stDim},
			synced,
		}
		if flux {
			cells = slices.Insert(cells, 3, cell{text: a.Kind, style: stDim})
		}
		if !row.deletedAt.IsZero() {
			for j := range cells {
				cells[j].style = stDeleted
			}
		}
		t.rows = append(t.rows, cells)
	}
	b.WriteString(t.render(g.cursor, &g.offset, l.tableRows+1, m.width))

	b.WriteString(m.changesPane(l.eventLines, len(g.events), &g.changesBack,
		func(i int) string { return m.appEventView(g.events[i]) },
		"no changes yet — sync, health and revision changes appear here"))
	b.WriteString(m.footer(jumpOr(g.jumpBuf, g.jumpAt, "[q/a] changes  [esc] back")))
	return b.String()
}

func (m *model) gitopsStatusLine() string {
	g := &m.gitops
	var green, amber, red int
	for _, row := range g.rows {
		switch row.app.Overall() {
		case gitops.Green:
			green++
		case gitops.Amber:
			amber++
		case gitops.Red:
			red++
		}
	}
	engine := string(g.engine)
	if engine == "" {
		engine = "…"
	}
	parts := []string{
		stBadge.Render("k7s"),
		"ctx " + stCyan.Render(m.info.Context),
		"gitops " + stCyan.Render(engine),
		fmt.Sprintf("apps %d %s %s %s", len(g.rows),
			stGreen.Render(fmt.Sprintf("●%d", green)),
			stAmber.Render(fmt.Sprintf("●%d", amber)),
			stRed.Render(fmt.Sprintf("●%d", red))),
	}
	switch {
	case g.err != nil && time.Since(g.errAt) < 30*time.Second:
		parts = append(parts, stRed.Render("watch error: "+g.err.Error()))
	case g.synced:
		parts = append(parts, m.pulse()+" "+stCyan.Render("watching"))
	case g.engine != gitops.None:
		parts = append(parts, stYellow.Render("○ syncing"))
	}
	return clip(strings.Join(parts, stDim.Render(" │ ")), m.width)
}

func (m *model) appEventView(e appEvent) string {
	kind := fmt.Sprintf("%-8s", e.kind)
	switch e.kind {
	case "ADDED":
		kind = stGreen.Render(kind)
	case "DELETED":
		kind = stRed.Render(kind)
	case string(stateKind):
		kind = stCyan.Render(kind)
	default:
		kind = stYellow.Render(kind)
	}
	state := lightStyle(e.app.SyncLight()).Render(e.app.Sync) + stDim.Render("/") +
		lightStyle(e.app.HealthLight()).Render(e.app.Health)
	s := fmt.Sprintf("%s %s %s  %s", stDim.Render(e.at.Format("15:04:05")), kind, e.app.Name, state)
	if e.detail != "" {
		s += stDim.Render("  " + e.detail)
	}
	for _, it := range e.items {
		s += "\n" + stYellow.Render("· "+it)
	}
	return s
}
