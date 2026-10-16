package pvs

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"

	"github.com/fu/k7s/internal/kube"
	"github.com/fu/k7s/internal/resources"
)

// Resource is the volumes kind. It watches every PersistentVolume of the
// cluster and shows those that belong to the page (relevant).
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

func (r *Resource) Name() string      { return "volumes" }
func (r *Resource) Tag() string       { return "pv" }
func (r *Resource) Objects() []string { return []string{"PersistentVolume"} }
func (r *Resource) States() []string  { return []string{"bound", "available", "released", "failed"} }
func (r *Resource) Available() bool   { return true }
func (r *Resource) Ready() bool       { return r.synced || r.err != nil }

func (r *Resource) Start(h resources.Host) tea.Cmd {
	r.Stop()
	r.host = h
	r.gen++
	gen := r.gen
	ctx, cancel := context.WithCancel(h.Context())
	r.cancel = cancel
	r.rows, r.order, r.synced, r.err = map[string]*rowState{}, nil, false, nil
	// Cluster-wide: PersistentVolumes have no namespace (and no app labels).
	err := kube.Watch(ctx, h.Client(), "", "",
		func(f informers.SharedInformerFactory) cache.SharedIndexInformer {
			return f.Core().V1().PersistentVolumes().Informer()
		},
		kube.Handlers[*corev1.PersistentVolume]{
			Object: func(o *corev1.PersistentVolume, deleted, initial bool) {
				h.Send(objMsg{gen, fromVolume(o), deleted, initial})
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

// relevant reports whether a volume belongs on the page: bound to a claim of
// the namespace, or with a filter, to a listed claim.
func (r *Resource) relevant(v Row) bool {
	if v.ClaimNamespace != r.host.Namespace() {
		return false
	}
	if r.host.Selector() == "" {
		return true
	}
	return slices.Contains(r.host.Listed(), v.claimRef())
}

// shown are the keys of the relevant volumes, in order.
func (r *Resource) shown() []string {
	var out []string
	for _, k := range r.order {
		if r.relevant(r.rows[k].row) {
			out = append(out, k)
		}
	}
	return out
}

// Listed is empty: which volumes are shown depends on what the others list
// (the claims), and nothing relates to the volumes in turn.
func (r *Resource) Listed() []resources.Ref { return nil }

// Links: a volume is bound to its claim (the claim links to it too; this
// covers a released volume whose claim is gone).
func (r *Resource) Links() []resources.Link {
	var out []resources.Link
	for _, k := range r.shown() {
		if v := r.rows[k].row; v.Claim != "" {
			out = append(out, resources.Link{From: v.claimRef(), To: v.ref()})
		}
	}
	return out
}

func (r *Resource) Empty() string {
	switch {
	case r.err != nil:
		return "cannot list volumes: " + r.err.Error()
	case !r.synced:
		return "waiting for volume list…"
	}
	return "no volumes bound to claims here"
}

// Status: the claims say how storage is; only errors here.
func (r *Resource) Status() []resources.Cell {
	if r.err != nil && time.Since(r.errAt) < 30*time.Second {
		return []resources.Cell{{Text: "volumes: " + r.err.Error(), Tone: resources.Bad}}
	}
	return nil
}

// ---- state and change log ------------------------------------------------------

// log adds a change; it shows while the volume belongs on the page.
func (r *Resource) log(kind resources.ChangeKind, v Row, detail string) {
	r.host.Log(resources.Change{At: time.Now(), Kind: kind, Object: v.Name, Detail: detail,
		State: []resources.Cell{{Text: v.Phase, Tone: v.Light()}},
		Show:  func() bool { return r.relevant(v) }})
}

func (r *Resource) apply(v Row, deleted, initial bool) {
	now := time.Now()
	key := v.Name
	rs, exists := r.rows[key]
	switch {
	case deleted:
		if exists {
			rs.row, rs.deletedAt = v, now
		}
		r.log(resources.Deleted, v, "")
	case !exists || !rs.deletedAt.IsZero():
		rs = &rowState{row: v, changed: map[string]time.Time{}}
		if !initial {
			rs.addedAt = now
			r.log(resources.Added, v, strings.TrimSpace(fmt.Sprintf("%s %s for %s", v.Capacity, v.Class, v.Claim)))
		}
		r.rows[key] = rs
	default:
		old := rs.row
		var details []string
		if old.Phase != v.Phase {
			rs.changed["STATUS"] = now
			details = append(details, fmt.Sprintf("status %s → %s", old.Phase, v.Phase))
		}
		if old.Capacity != v.Capacity {
			rs.changed["CAPACITY"] = now
			details = append(details, fmt.Sprintf("resized %s → %s", old.Capacity, v.Capacity))
		}
		if v.Reason != "" && v.Reason != old.Reason {
			details = append(details, v.Reason)
		}
		rs.row = v
		if len(details) > 0 {
			r.log(resources.Modified, v, strings.Join(details, ", "))
		}
	}
	r.resort()
}

// logInitialProblems explains released and failed volumes at start.
func (r *Resource) logInitialProblems() {
	for _, k := range r.order {
		v := r.rows[k].row
		switch v.Light() {
		case resources.Alarm:
			r.log(resources.State, v, v.Reason)
		case resources.Warn:
			r.log(resources.State, v, "claim "+v.Claim+" is gone, reclaim policy "+v.Reclaim)
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
