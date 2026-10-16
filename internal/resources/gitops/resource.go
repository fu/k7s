package gitops

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/fu/k7s/internal/kube"
	"github.com/fu/k7s/internal/resources"
)

// Resource is the gitops kind. The watch covers all apps of the cluster, so
// parents (app of apps) are known too; it only restarts with the context.
type Resource struct {
	host   resources.Host
	client *kube.Client // the cluster being watched
	gen    int
	cancel context.CancelFunc

	detected bool
	engine   Engine
	rows     map[string]*appRow
	order    []string
	synced   bool
	err      error
	errAt    time.Time

	// responsible is asked for every app change in the log on every frame,
	// so it is cached briefly.
	resp   []string
	respAt time.Time
}

func New() *Resource { return &Resource{} }

const keepDeleted = 8 * time.Second

type (
	detectedMsg struct {
		gen    int
		engine Engine
	}
	appMsg struct {
		gen int
		ev  Event
	}
	syncedMsg struct{ gen int }
	errMsg    struct {
		gen int
		err error
	}
)

type appRow struct {
	app           App
	syncChanged   time.Time
	healthChanged time.Time
	deletedAt     time.Time
}

func (r *Resource) Name() string    { return "gitops" }
func (r *Resource) Available() bool { return r.detected && r.engine != None }
func (r *Resource) Ready() bool     { return r.detected && (r.engine == None || r.synced) }

// The apps relate to what others list; nothing relates to them.
func (r *Resource) Listed() []resources.Ref { return nil }

func (r *Resource) Actions(string) []resources.Action { return nil }

func (r *Resource) Empty() string { return "no app deploys what is listed here" }

// Start detects the engine of a new cluster and watches it. On a namespace
// or filter change the watch stays; only the problems are explained again,
// for the new page's change log.
func (r *Resource) Start(h resources.Host) tea.Cmd {
	r.host = h
	r.respAt = time.Time{}
	if h.Client() == r.client {
		if r.synced {
			r.logInitialProblems()
		}
		return nil
	}
	r.Stop()
	r.client = h.Client()
	r.gen++
	gen, client := r.gen, r.client
	r.detected, r.engine, r.synced, r.err = false, None, false, nil
	r.rows, r.order = map[string]*appRow{}, nil
	return func() tea.Msg { return detectedMsg{gen, Detect(client)} }
}

func (r *Resource) Stop() {
	if r.cancel != nil {
		r.cancel()
	}
}

func (r *Resource) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case detectedMsg:
		if msg.gen != r.gen {
			return nil
		}
		r.detected, r.engine = true, msg.engine
		if msg.engine == None {
			return nil
		}
		ctx, cancel := context.WithCancel(r.host.Context())
		r.cancel = cancel
		gen, send := r.gen, r.host.Send
		err := Watch(ctx, r.client, msg.engine, Handlers{
			App:    func(e Event) { send(appMsg{gen, e}) },
			Synced: func() { send(syncedMsg{gen}) },
			Error:  func(err error) { send(errMsg{gen, err}) },
		})
		if err != nil {
			r.err, r.errAt = err, time.Now()
		}
	case appMsg:
		if msg.gen == r.gen {
			r.apply(msg.ev)
		}
	case syncedMsg:
		if msg.gen == r.gen && !r.synced {
			r.synced = true
			r.logInitialProblems()
		}
	case errMsg:
		if msg.gen == r.gen {
			r.err, r.errAt = msg.err, time.Now()
		}
	}
	return nil
}

// apps returns the current apps by key, and which app deploys which.
func (r *Resource) apps() (map[string]App, map[string]string) {
	apps := make(map[string]App, len(r.rows))
	for k, row := range r.rows {
		if row.deletedAt.IsZero() {
			apps[k] = row.app
		}
	}
	return apps, Parents(apps)
}

// responsible lists the apps responsible for what is shown: those deploying
// something another section lists (a secret, a pod's workload). Without a
// filter, also every app deploying into the namespace.
func (r *Resource) responsible() []string {
	if !r.Available() {
		return nil
	}
	if time.Since(r.respAt) > 200*time.Millisecond {
		r.resp, r.respAt = r.findResponsible(), time.Now()
	}
	return r.resp
}

func (r *Resource) findResponsible() []string {
	ns, all := r.host.Namespace(), r.host.Selector() == ""
	listed := map[Ref]bool{}
	for _, ref := range r.host.Listed() {
		listed[Ref(ref)] = true
	}
	var keys []string
	for k, row := range r.rows {
		if isResponsible(row.app, listed, ns, all) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
}

func isResponsible(a App, listed map[Ref]bool, ns string, all bool) bool {
	if all && a.DestNS == ns {
		return true
	}
	for _, ref := range a.Manages {
		if listed[ref] || (all && ref.Namespace == ns) {
			return true
		}
	}
	return false
}

// Status is one light for the whole deployment chain: an all-green pod list
// under a broken app (or a broken parent app) must not look fine.
func (r *Resource) Status() []resources.Cell {
	var s []resources.Cell
	if keys := r.responsible(); len(keys) > 0 {
		apps, parents := r.apps()
		worst := Grey
		for _, k := range keys {
			worst = max(worst, apps[k].Overall(), ChainLight(k, apps, parents))
		}
		s = append(s, resources.Cell{Text: "gitops "}, resources.Cell{Text: "●", Tone: resources.Light(int(worst))},
			resources.Cell{Text: fmt.Sprintf(" %d apps", len(keys))})
	}
	if r.err != nil && time.Since(r.errAt) < 30*time.Second {
		s = append(s, resources.Cell{Text: " gitops: " + r.err.Error(), Tone: resources.Bad})
	}
	return s
}

// ---- state and change log ------------------------------------------------------

func (r *Resource) apply(e Event) {
	now := time.Now()
	key := e.App.Key()
	row, exists := r.rows[key]
	switch {
	case e.Type == Deleted:
		if exists {
			row.app, row.deletedAt = e.App, now
		}
		r.log(now, resources.Deleted, e.App, "")
	case !exists || !row.deletedAt.IsZero():
		r.rows[key] = &appRow{app: e.App}
		if !e.Initial {
			r.log(now, resources.Added, e.App, "")
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
			from := old.Revision
			if from == "" {
				from = "<none>"
			}
			details = append(details, fmt.Sprintf("revision %s → %s", from, e.App.Revision))
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
		if e.App.Overall() == Red && old.Overall() != Red {
			details = append(details, e.App.Explain())
			items = e.App.Problems
		}
		row.app = e.App
		details = slices.DeleteFunc(details, func(s string) bool { return s == "" })
		if len(details) > 0 {
			r.log(now, resources.Modified, e.App, strings.Join(details, ", "), items...)
		}
	}
	r.resort()
}

// logInitialProblems explains every app that is not green.
func (r *Resource) logInitialProblems() {
	now := time.Now()
	for _, k := range r.order {
		if row := r.rows[k]; row.app.Overall() != Green && row.deletedAt.IsZero() {
			r.log(now, resources.State, row.app, row.app.Explain(), row.app.Problems...)
		}
	}
}

// log adds an app change; it shows while the app is responsible for the page.
func (r *Resource) log(at time.Time, kind resources.ChangeKind, a App, detail string, items ...string) {
	key := a.Key()
	r.host.Log(resources.Change{
		At: at, Kind: kind, Object: a.Name, Detail: detail, Items: items,
		State: []resources.Cell{
			{Text: a.Sync, Tone: resources.Light(int(a.SyncLight()))},
			{Text: "/", Tone: resources.Dim},
			{Text: a.Health, Tone: resources.Light(int(a.HealthLight()))},
		},
		Show: func() bool { return slices.Contains(r.responsible(), key) },
	})
}

func (r *Resource) resort() {
	r.order = r.order[:0]
	for k, row := range r.rows {
		if !row.deletedAt.IsZero() && time.Since(row.deletedAt) > keepDeleted {
			delete(r.rows, k)
			continue
		}
		r.order = append(r.order, k)
	}
	sort.Strings(r.order)
}
