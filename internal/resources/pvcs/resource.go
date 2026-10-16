package pvcs

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"

	"github.com/fu/k7s/internal/kube"
	"github.com/fu/k7s/internal/resources"
)

// Resource is the claims kind.
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

func (r *Resource) Name() string      { return "claims" }
func (r *Resource) Key() string       { return "m" } // c is contexts
func (r *Resource) Tag() string       { return "pvc" }
func (r *Resource) Objects() []string { return []string{"PersistentVolumeClaim"} }
func (r *Resource) States() []string  { return []string{"bound", "pending", "lost"} }
func (r *Resource) GVR() schema.GroupVersionResource {
	return corev1.SchemeGroupVersion.WithResource("persistentvolumeclaims")
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
			return f.Core().V1().PersistentVolumeClaims().Informer()
		},
		kube.Handlers[*corev1.PersistentVolumeClaim]{
			Object: func(o *corev1.PersistentVolumeClaim, deleted, initial bool) {
				h.Send(objMsg{gen, fromClaim(o), deleted, initial})
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

// Links: a claim leads to the volume it is bound to.
func (r *Resource) Links() []resources.Link {
	var out []resources.Link
	for _, k := range r.order {
		if c := r.rows[k].row; c.Volume != "" {
			out = append(out, resources.Link{From: c.ref(), To: resources.Ref{Kind: "PersistentVolume", Name: c.Volume}})
		}
	}
	return out
}

func (r *Resource) Empty() string {
	if !r.synced {
		return "waiting for claim list…"
	}
	return "no volume claims"
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
		{Text: fmt.Sprintf("claims %d ", len(r.order))},
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

func (r *Resource) log(kind resources.ChangeKind, c Row, detail string) {
	r.host.Log(resources.Change{At: time.Now(), Kind: kind, Object: c.Name, Detail: detail,
		State: []resources.Cell{{Text: c.Phase, Tone: c.Light()}}})
}

func (r *Resource) apply(c Row, deleted, initial bool) {
	now := time.Now()
	key := c.Key()
	rs, exists := r.rows[key]
	switch {
	case deleted:
		if exists {
			rs.row, rs.deletedAt = c, now
		}
		r.log(resources.Deleted, c, "")
	case !exists || !rs.deletedAt.IsZero():
		rs = &rowState{row: c, changed: map[string]time.Time{}}
		if !initial {
			rs.addedAt = now
			r.log(resources.Added, c, strings.TrimSpace(fmt.Sprintf("%s %s %s", c.Capacity, c.Access, c.Class)))
		}
		r.rows[key] = rs
	default:
		old := rs.row
		var details []string
		if old.Phase != c.Phase {
			rs.changed["STATUS"] = now
			d := fmt.Sprintf("status %s → %s", old.Phase, c.Phase)
			if c.Volume != "" && old.Volume != c.Volume {
				d += " (volume " + c.Volume + ")"
			}
			details = append(details, d)
		}
		if old.Capacity != c.Capacity {
			rs.changed["CAPACITY"] = now
			details = append(details, fmt.Sprintf("resized %s → %s", old.Capacity, c.Capacity))
		}
		if c.Resizing && !old.Resizing {
			details = append(details, "resizing")
		}
		rs.row = c
		if len(details) > 0 {
			r.log(resources.Modified, c, strings.Join(details, ", "))
		}
	}
	r.resort()
}

// logInitialProblems lists the claims that are not bound at start; the
// Kubernetes events in the log say why (e.g. ProvisioningFailed).
func (r *Resource) logInitialProblems() {
	for _, k := range r.order {
		if c := r.rows[k].row; c.Light() != resources.Good {
			r.log(resources.State, c, strings.TrimSpace(c.Capacity+" "+c.Class))
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
