package replicasets

import (
	"context"
	"fmt"
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

// Resource is the replicasets kind.
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

func (r *Resource) Name() string      { return "replicasets" }
func (r *Resource) Tag() string       { return "rs" }
func (r *Resource) Objects() []string { return []string{"ReplicaSet"} }
func (r *Resource) GVR() schema.GroupVersionResource {
	return appsv1.SchemeGroupVersion.WithResource("replicasets")
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
			return f.Apps().V1().ReplicaSets().Informer()
		},
		kube.Handlers[*appsv1.ReplicaSet]{
			Object: func(o *appsv1.ReplicaSet, deleted, initial bool) {
				h.Send(objMsg{gen, fromReplicaSet(o), deleted, initial})
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

// Links: its Deployment runs the ReplicaSet (which runs the pods; they say so).
func (r *Resource) Links() []resources.Link {
	var out []resources.Link
	for _, k := range r.order {
		s := r.rows[k].row
		if s.OwnerKind != "" {
			out = append(out, resources.Link{From: resources.Ref{Kind: s.OwnerKind, Namespace: s.Namespace, Name: s.OwnerName}, To: s.ref()})
		}
	}
	return out
}

func (r *Resource) Empty() string {
	if !r.synced {
		return "waiting for replicaset list…"
	}
	return "no replicasets"
}

// Status: the deployments say how the rollouts are; only errors here.
func (r *Resource) Status() []resources.Cell {
	if r.err != nil && time.Since(r.errAt) < 30*time.Second {
		return []resources.Cell{{Text: "replicasets: " + r.err.Error(), Tone: resources.Bad}}
	}
	return nil
}

// ---- state and change log ------------------------------------------------------

func (r *Resource) log(kind resources.ChangeKind, s Row, detail string) {
	r.host.Log(resources.Change{At: time.Now(), Kind: kind, Object: s.Name, Detail: detail})
}

// apply logs what a rollout does to its ReplicaSets: a new revision, scaling
// up and down, and failing to create pods. Readiness is the pods' story.
func (r *Resource) apply(s Row, deleted, initial bool) {
	now := time.Now()
	key := s.Key()
	rs, exists := r.rows[key]
	switch {
	case deleted:
		if exists {
			rs.row, rs.deletedAt = s, now
		}
		r.log(resources.Deleted, s, "")
	case !exists || !rs.deletedAt.IsZero():
		rs = &rowState{row: s, changed: map[string]time.Time{}}
		if !initial {
			rs.addedAt = now
			detail := fmt.Sprintf("%d replicas", s.Desired)
			if s.Revision != "" {
				detail = "revision " + s.Revision + ", " + detail
			}
			r.log(resources.Added, s, detail)
		}
		r.rows[key] = rs
	default:
		old := rs.row
		var details []string
		if old.Desired != s.Desired {
			rs.changed["DESIRED"] = now
			details = append(details, fmt.Sprintf("scaled %d → %d", old.Desired, s.Desired))
		}
		if old.Current != s.Current {
			rs.changed["CURRENT"] = now
		}
		if old.Ready != s.Ready {
			rs.changed["READY"] = now
		}
		if s.Failure != "" && s.Failure != old.Failure {
			details = append(details, s.Failure)
		}
		rs.row = s
		if len(details) > 0 {
			r.log(resources.Modified, s, strings.Join(details, ", "))
		}
	}
	r.resort()
}

// logInitialProblems explains every ReplicaSet that cannot create its pods.
func (r *Resource) logInitialProblems() {
	for _, k := range r.order {
		if s := r.rows[k].row; s.Failure != "" {
			r.log(resources.State, s, s.Failure)
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
