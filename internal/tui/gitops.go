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

// GitOps is a section of the main page: the Argo CD / Flux apps responsible
// for what is listed. The watch itself covers all apps of the cluster, so
// parents (app of apps) are known too.

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

	rows   map[string]*appRow
	order  []string
	events []appEvent
	synced bool
	err    error
	errAt  time.Time
}

// startGitOps detects the engine of the current context and starts watching.
func (m *model) startGitOps() tea.Cmd {
	g := &m.gitops
	if g.cancel != nil {
		g.cancel()
	}
	gen := g.gen + 1
	*g = gitopsState{gen: gen, rows: map[string]*appRow{}}
	client := m.client
	return func() tea.Msg { return gitopsDetectedMsg{gen, gitops.Detect(client)} }
}

// apps returns the current apps by key, and which app deploys which.
func (g *gitopsState) apps() (map[string]gitops.App, map[string]string) {
	apps := make(map[string]gitops.App, len(g.rows))
	for k, row := range g.rows {
		if row.deletedAt.IsZero() {
			apps[k] = row.app
		}
	}
	return apps, gitops.Parents(apps)
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

// gitopsTable is the apps table for the given app keys. PARENT shows the app
// that deploys this one, colored by the worst light up that chain, so a
// green app under a broken parent does not look fine.
func (m *model) gitopsTable(keys []string) table {
	g := &m.gitops
	apps, parents := g.apps()
	// Flux mixes Kustomizations and HelmReleases, so it gets a KIND column.
	flux := g.engine == gitops.Flux
	t := table{
		headers: []string{"#", "SYNC", "HEALTH", "NAME", "DEST", "PARENT", "SOURCE", "REV", "SYNCED"},
		right:   []bool{true, false, false, false, false, false, false, false, false},
		flex:    6,
	}
	if flux {
		t.headers = slices.Insert(t.headers, 3, "KIND")
		t.right = slices.Insert(t.right, 3, false)
		t.flex = 7
	}
	now := time.Now()
	for i, k := range keys {
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
		synced := cell{text: "-", style: stDim}
		if !a.SyncedAt.IsZero() {
			synced = cell{text: pods.Age(a.SyncedAt), style: stDim}
		}
		parent := cell{}
		if p, ok := parents[k]; ok {
			parent = cell{text: "● " + apps[p].Name, style: lightStyle(gitops.ChainLight(k, apps, parents)), middle: true}
		}
		cells := []cell{
			{text: fmt.Sprint(i + 1), style: stDim},
			sync, health,
			{text: a.Name, middle: true},
			{text: a.DestNS, style: stDim},
			parent,
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
	return t
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
