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

func (r *Resource) Name() string { return "gitops" }

// Tag: ap (Argo CD Application); Flux changes say ks or hr themselves.
func (r *Resource) Tag() string       { return "ap" }
func (r *Resource) Objects() []string { return []string{"Application", "Kustomization", "HelmRelease"} }

// tag is an app's tag: kubectl's short names for the Flux kinds.
func tag(a App) string {
	switch a.Kind {
	case "Kustomization":
		return "ks"
	case "HelmRelease":
		return "hr"
	}
	return "ap"
}

func (r *Resource) Available() bool { return r.detected && r.engine != None }
func (r *Resource) Ready() bool     { return r.detected && (r.engine == None || r.synced) }

// The apps relate to what others list; nothing relates to them.
func (r *Resource) Listed() []resources.Ref { return nil }

func (r *Resource) Empty() string { return "no app deploys what is listed here" }

// Start detects the engine of a new cluster and watches it. On a namespace
// or filter change the watch stays; only the problems are explained again,
// for the new page's change log.
func (r *Resource) Start(h resources.Host) tea.Cmd {
	r.host = h
	r.respAt = time.Time{}
	if h.Client() == r.client {
		if r.synced {
			return r.logInitialProblems()
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
			return r.apply(msg.ev)
		}
	case syncedMsg:
		if msg.gen == r.gen && !r.synced {
			r.synced = true
			return r.logInitialProblems()
		}
	case explainedMsg:
		if msg.gen == r.gen {
			r.log(msg.at, msg.kind, msg.app, msg.detail, msg.items...)
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

// ---- state and change log ------------------------------------------------------

func (r *Resource) apply(e Event) tea.Cmd {
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
		old, a := row.app, e.App
		var details []string
		if old.Sync != a.Sync {
			row.syncChanged = now
			details = append(details, fmt.Sprintf("sync %s → %s", old.Sync, a.Sync))
		}
		if old.Health != a.Health {
			row.healthChanged = now
			details = append(details, fmt.Sprintf("health %s → %s", old.Health, a.Health))
		}
		if old.Revision != a.Revision && a.Revision != "" {
			from := old.Revision
			if from == "" {
				from = "<none>"
			}
			details = append(details, fmt.Sprintf("revision %s → %s", from, a.Revision))
		}
		if old.OpPhase != a.OpPhase && a.OpPhase != "" {
			op := "sync " + strings.ToLower(a.OpPhase)
			if a.OpPhase != "Succeeded" && a.OpMessage != "" {
				op += ": " + a.OpMessage
			}
			details = append(details, op)
		}
		// Say what broke and what got fixed, resource by resource.
		var items []string
		for _, p := range a.Problems {
			if !slices.Contains(old.Problems, p) {
				items = append(items, p)
			}
		}
		for _, p := range old.Problems {
			if !slices.Contains(a.Problems, p) {
				items = append(items, "fixed: "+p)
			}
		}
		if a.Overall() != Green && a.Explain() != old.Explain() {
			details = append(details, a.Explain())
		}
		row.app = a
		details = slices.DeleteFunc(details, func(s string) bool { return s == "" })
		switch {
		case a.Overall() > old.Overall():
			// It got worse: find out why if Argo CD / Flux does not say.
			r.resort()
			return r.explain(now, resources.Modified, a, strings.Join(details, ", "), items)
		case len(details) > 0 || len(items) > 0:
			r.log(now, resources.Modified, a, strings.Join(details, ", "), items...)
		}
	}
	r.resort()
	return nil
}

// logInitialProblems explains every app that is not green: why, and every
// resource that is not healthy or not in sync.
func (r *Resource) logInitialProblems() tea.Cmd {
	now := time.Now()
	var cmds []tea.Cmd
	for _, k := range r.order {
		if row := r.rows[k]; row.app.Overall() != Green && row.deletedAt.IsZero() {
			a := row.app
			why := a.Explain()
			if why == "" {
				why = strings.ToLower(a.Sync + ", " + a.Health)
			}
			cmds = append(cmds, r.explain(now, resources.State, a, why, nil))
		}
	}
	return tea.Batch(cmds...)
}

// explainedMsg brings an app change back with its diagnosis.
type explainedMsg struct {
	gen    int
	at     time.Time
	kind   resources.ChangeKind
	app    App
	detail string
	items  []string
}

// explain logs a change of an app that is not green with everything that
// says why: the problem resources Argo CD / Flux list, and what k7s finds
// looking at what the app manages itself (child apps, workloads, their
// failing pods) — Argo CD 3 keeps resource health elsewhere, so its list is
// often empty. The entry is logged once that look is done.
func (r *Resource) explain(at time.Time, kind resources.ChangeKind, a App, detail string, items []string) tea.Cmd {
	add := func(list []string) {
		for _, p := range list {
			if !slices.Contains(items, p) {
				items = append(items, p)
			}
		}
	}
	add(a.Problems)
	if a.Overall() == Green {
		r.log(at, kind, a, detail, items...)
		return nil
	}
	gen, ctx, client := r.gen, r.host.Context(), r.client
	apps, _ := r.apps()
	return func() tea.Msg {
		add(diagnose(ctx, client, a, apps))
		if len(items) == 0 {
			items = []string{"no reason given, and none of its workloads or child apps looks unhealthy"}
		}
		return explainedMsg{gen, at, kind, a, detail, items}
	}
}

// log adds an app change; it shows while the app is responsible for the page.
// The app's lights are on its row; the entry says what happened and why.
func (r *Resource) log(at time.Time, kind resources.ChangeKind, a App, detail string, items ...string) {
	key := a.Key()
	r.host.Log(resources.Change{
		At: at, Tag: tag(a), Kind: kind, Object: a.Name, Detail: detail, Items: items,
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
