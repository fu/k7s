package deployments

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"

	"github.com/fu/k7s/internal/kube"
	"github.com/fu/k7s/internal/resources"
)

// Resource is the deployments kind.
type Resource struct {
	host   resources.Host
	gen    int
	cancel context.CancelFunc

	rows   map[string]*rowState
	order  []string
	synced bool
	err    error
	errAt  time.Time
}

func New() *Resource { return &Resource{} }

const keepDeleted = 8 * time.Second

type (
	objMsg struct {
		gen              int
		row              Row
		deleted, initial bool
	}
	syncedMsg struct{ gen int }
	errMsg    struct {
		gen int
		err error
	}
	tickMsg struct{ gen int }
)

type rowState struct {
	row       Row
	changed   map[string]time.Time // column title → when it last changed
	addedAt   time.Time
	deletedAt time.Time
}

func (r *Resource) Name() string      { return "deployments" }
func (r *Resource) Key() string       { return "e" } // d is describe
func (r *Resource) Tag() string       { return "dp" }
func (r *Resource) Objects() []string { return []string{"Deployment"} }
func (r *Resource) GVR() schema.GroupVersionResource {
	return appsv1.SchemeGroupVersion.WithResource("deployments")
}
func (r *Resource) Available() bool { return true }
func (r *Resource) Ready() bool     { return r.synced }

func (r *Resource) Start(h resources.Host) tea.Cmd {
	r.Stop()
	r.host = h
	r.gen++
	gen := r.gen
	ctx, cancel := context.WithCancel(h.Context())
	r.cancel = cancel
	r.rows, r.order, r.synced, r.err = map[string]*rowState{}, nil, false, nil
	err := kube.Watch(ctx, h.Client(), h.Namespace(), h.Selector(),
		func(f informers.SharedInformerFactory) cache.SharedIndexInformer {
			return f.Apps().V1().Deployments().Informer()
		},
		kube.Handlers[*appsv1.Deployment]{
			Object: func(o *appsv1.Deployment, deleted, initial bool) {
				h.Send(objMsg{gen, fromDeployment(o), deleted, initial})
			},
			Synced: func() { h.Send(syncedMsg{gen}) },
			Error:  func(err error) { h.Send(errMsg{gen, err}) },
		})
	if err != nil {
		r.err, r.errAt = err, time.Now()
	}
	return tick(gen)
}

func (r *Resource) Stop() {
	if r.cancel != nil {
		r.cancel()
	}
}

func tick(gen int) tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg { return tickMsg{gen} })
}

func (r *Resource) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case objMsg:
		if msg.gen == r.gen {
			r.apply(msg.row, msg.deleted, msg.initial)
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
	case tickMsg:
		if msg.gen == r.gen {
			r.purgeDeleted()
			return tick(r.gen)
		}
	}
	return nil
}

func (r *Resource) Listed() []resources.Ref {
	var out []resources.Ref
	for _, k := range r.order {
		out = append(out, r.rows[k].row.ref())
	}
	return out
}

// Links: a Deployment's pods use the secrets and volume claims of its
// template.
func (r *Resource) Links() []resources.Link {
	var out []resources.Link
	for _, k := range r.order {
		d := r.rows[k].row
		for _, s := range d.Secrets {
			out = append(out, resources.Link{From: d.ref(), To: resources.Ref{Kind: "Secret", Namespace: d.Namespace, Name: s}})
		}
		for _, c := range d.Claims {
			out = append(out, resources.Link{From: d.ref(), To: resources.Ref{Kind: "PersistentVolumeClaim", Namespace: d.Namespace, Name: c}})
		}
	}
	return out
}

func (r *Resource) Empty() string {
	if !r.synced {
		return "waiting for deployment list…"
	}
	return "no deployments"
}

func (r *Resource) Status() []resources.Cell {
	if len(r.order) == 0 && r.err == nil {
		return nil
	}
	var good, busy, bad int
	for _, rs := range r.rows {
		if rs.deletedAt.IsZero() {
			switch rs.row.Light() {
			case resources.Good:
				good++
			case resources.Busy:
				busy++
			case resources.Alarm:
				bad++
			}
		}
	}
	s := []resources.Cell{
		{Text: fmt.Sprintf("deployments %d ", len(r.order))},
		{Text: fmt.Sprintf("●%d ", good), Tone: resources.Good},
		{Text: fmt.Sprintf("●%d ", busy), Tone: resources.Busy},
		{Text: fmt.Sprintf("●%d", bad), Tone: resources.Bad},
	}
	if r.err != nil && time.Since(r.errAt) < 30*time.Second {
		s = append(s, resources.Cell{Text: " watch error: " + r.err.Error(), Tone: resources.Bad})
	}
	return s
}

// ---- state and change log ------------------------------------------------------

func (r *Resource) log(kind resources.ChangeKind, d Row, detail string) {
	r.host.Log(resources.Change{At: time.Now(), Kind: kind, Object: d.Name, Detail: detail,
		State: []resources.Cell{{Text: d.ReadyString(), Tone: d.Light()}}})
}

func (r *Resource) apply(d Row, deleted, initial bool) {
	now := time.Now()
	key := d.Key()
	rs, exists := r.rows[key]
	switch {
	case deleted:
		if exists {
			rs.row, rs.deletedAt = d, now
		}
		r.log(resources.Deleted, d, "")
	case !exists || !rs.deletedAt.IsZero():
		rs = &rowState{row: d, changed: map[string]time.Time{}}
		if !initial {
			rs.addedAt = now
			r.log(resources.Added, d, "images "+strings.Join(d.Images, ", "))
		}
		r.rows[key] = rs
	default:
		old := rs.row
		var details []string
		mark := func(col, what string, from, to int32) {
			if from != to {
				rs.changed[col] = now
				details = append(details, fmt.Sprintf("%s %d → %d", what, from, to))
			}
		}
		// A new image is a new rollout: say so first.
		if !slices.Equal(old.Images, d.Images) {
			rs.changed["IMAGES"] = now
			details = append(details, fmt.Sprintf("rollout %s → %s", strings.Join(old.Images, ", "), strings.Join(d.Images, ", ")))
		}
		mark("READY", "replicas", old.Desired, d.Desired)
		mark("READY", "ready", old.Ready, d.Ready)
		mark("UP-TO-DATE", "up-to-date", old.Updated, d.Updated)
		mark("AVAILABLE", "available", old.Available, d.Available)
		if old.Paused != d.Paused {
			details = append(details, map[bool]string{true: "rollout paused", false: "rollout resumed"}[d.Paused])
		}
		if d.Stuck != "" && d.Stuck != old.Stuck {
			details = append(details, d.Stuck)
		}
		if d.Progress == "NewReplicaSetAvailable" && old.Progress != d.Progress && old.Progress != "" {
			details = append(details, "rollout complete")
		}
		rs.row = d
		if len(details) > 0 {
			r.log(resources.Modified, d, strings.Join(details, ", "))
		}
	}
	r.resort()
}

// logInitialProblems explains every deployment not fine at start.
func (r *Resource) logInitialProblems() {
	for _, k := range r.order {
		d := r.rows[k].row
		switch d.Light() {
		case resources.Alarm:
			r.log(resources.State, d, d.Stuck)
		case resources.Busy:
			r.log(resources.State, d, fmt.Sprintf("%d of %d ready, %d up to date, %d available", d.Ready, d.Desired, d.Updated, d.Available))
		}
	}
}

func (r *Resource) purgeDeleted() {
	removed := false
	for k, rs := range r.rows {
		if !rs.deletedAt.IsZero() && time.Since(rs.deletedAt) > keepDeleted {
			delete(r.rows, k)
			removed = true
		}
	}
	if removed {
		r.resort()
	}
}

func (r *Resource) resort() {
	r.order = r.order[:0]
	for k := range r.rows {
		r.order = append(r.order, k)
	}
	sort.Strings(r.order)
}
