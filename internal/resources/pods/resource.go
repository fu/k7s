package pods

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/fu/k7s/internal/resources"
)

// Resource is the pods kind.
type Resource struct {
	host   resources.Host
	gen    int
	cancel context.CancelFunc

	rows           map[string]*rowState
	usage          map[string]Usage
	containerUsage map[string]map[string]Usage
	order          []string // sorted by name
	synced         bool
	metricsErr     error
	watchErr       error
	watchErrAt     time.Time

	// Port-forwards outlive namespace switches; they end when k7s quits.
	forwards map[string]*forward
}

func New() *Resource { return &Resource{forwards: map[string]*forward{}} }

const keepDeleted = 8 * time.Second // how long a deleted pod lingers (struck through)

// Messages carry the generation of the watch that sent them, so late ones
// from a namespace already switched away from are ignored.
type (
	podMsg struct {
		gen int
		ev  Event
	}
	metricsMsg struct {
		gen int
		m   Metrics
	}
	syncedMsg   struct{ gen int }
	watchErrMsg struct {
		gen int
		err error
	}
	tickMsg struct{ gen int }
)

type rowState struct {
	row       Row
	usage     Usage
	hasUsage  bool
	cpuTrend  int // -1, 0, +1 versus the previous metrics poll
	memTrend  int
	changed   map[string]time.Time // column title → when it last changed
	addedAt   time.Time
	deletedAt time.Time
}

func (r *Resource) Name() string      { return "pods" }
func (r *Resource) Tag() string       { return "po" }
func (r *Resource) Objects() []string { return []string{"Pod"} }
func (r *Resource) GVR() schema.GroupVersionResource {
	return corev1.SchemeGroupVersion.WithResource("pods")
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
	r.rows, r.order, r.synced = map[string]*rowState{}, nil, false
	r.usage, r.containerUsage = map[string]Usage{}, map[string]map[string]Usage{}
	r.metricsErr, r.watchErr = nil, nil

	err := Watch(ctx, h.Client(), h.Namespace(), h.Selector(), h.Refresh(), Handlers{
		Pod:     func(e Event) { h.Send(podMsg{gen, e}) },
		Metrics: func(m Metrics) { h.Send(metricsMsg{gen, m}) },
		Synced:  func() { h.Send(syncedMsg{gen}) },
		Error:   func(err error) { h.Send(watchErrMsg{gen, err}) },
	})
	if err != nil {
		r.watchErr, r.watchErrAt = err, time.Now()
	}
	return tick(gen)
}

func (r *Resource) Stop() {
	if r.cancel != nil {
		r.cancel()
	}
}

// tick drives the purge of deleted pods, one loop per watch.
func tick(gen int) tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg { return tickMsg{gen} })
}

func (r *Resource) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case podMsg:
		if msg.gen == r.gen {
			r.apply(msg.ev)
		}
	case metricsMsg:
		if msg.gen == r.gen {
			r.applyMetrics(msg.m)
		}
	case syncedMsg:
		if msg.gen == r.gen && !r.synced {
			r.synced = true
			r.logInitialProblems()
		}
	case watchErrMsg:
		if msg.gen == r.gen {
			r.watchErr, r.watchErrAt = msg.err, time.Now()
		}
	case tickMsg:
		if msg.gen == r.gen {
			r.purgeDeleted()
			return tick(r.gen)
		}
	case fwdReadyMsg, fwdEndMsg:
		r.forwardMsg(msg)
	}
	return nil
}

// Pod returns the current state of the pod with key, for commands.
func (r *Resource) Pod(key string) (Row, bool) {
	rs, ok := r.rows[key]
	if !ok {
		return Row{}, false
	}
	return rs.row, true
}

// ContainerUsage returns the last metrics of a pod's containers by name.
func (r *Resource) ContainerUsage(key string) map[string]Usage { return r.containerUsage[key] }

// Listed returns the pods and the workloads running them.
func (r *Resource) Listed() []resources.Ref {
	var out []resources.Ref
	for _, k := range r.order {
		p := r.rows[k].row
		out = append(out, p.ref())
		if p.WorkloadKind != "" {
			out = append(out, resources.Ref{Kind: p.WorkloadKind, Namespace: p.Namespace, Name: p.WorkloadName})
		}
	}
	return out
}

func (r *Resource) Empty() string {
	if !r.synced {
		return "waiting for pod list…"
	}
	return "no pods"
}

// ---- state and change log ------------------------------------------------------

func (r *Resource) log(at time.Time, kind resources.ChangeKind, p Row, detail string) {
	r.host.Log(resources.Change{At: at, Kind: kind, Object: p.Name, Detail: detail, State: []resources.Cell{
		{Text: p.Status, Tone: light(p)}, {Text: " " + p.ReadyString()},
	}})
}

func (r *Resource) apply(e Event) {
	now := time.Now()
	key := e.Row.Key()
	rs, exists := r.rows[key]

	switch {
	case e.Type == Deleted:
		if exists {
			rs.row, rs.deletedAt = e.Row, now
		}
		r.log(now, resources.Deleted, e.Row, "")

	case !exists || !rs.deletedAt.IsZero():
		// New pod, or a pod re-created under the same name (StatefulSets).
		rs = &rowState{row: e.Row, changed: map[string]time.Time{}}
		if u, ok := r.usage[key]; ok {
			rs.usage, rs.hasUsage = u, true
		}
		if !e.Initial {
			rs.addedAt = now
			r.log(now, resources.Added, e.Row, "")
		}
		r.rows[key] = rs

	default:
		var details []string
		mark := func(col, name, from, to string) {
			if from != to {
				rs.changed[col] = now
				details = append(details, fmt.Sprintf("%s %s → %s", name, orNone(from), orNone(to)))
			}
		}
		old := rs.row
		mark("STATUS", "status", old.Status, e.Row.Status)
		if msg := e.Row.WaitMessage(); msg != "" && old.Status != e.Row.Status {
			details = append(details, msg)
		}
		mark("READY", "ready", old.ReadyString(), e.Row.ReadyString())
		mark("RESTARTS", "restarts", fmt.Sprint(old.Restarts), fmt.Sprint(e.Row.Restarts))
		if old.Node != e.Row.Node && e.Row.Node != "" {
			details = append(details, "scheduled on "+e.Row.Node)
		}
		// Explain a new container exit: why it stopped.
		if le := e.Row.LastExit; !le.IsZero() && !le.At.Equal(old.LastExit.At) {
			details = append(details, "exit "+e.Row.exitWho()+le.Describe())
		}
		rs.row = e.Row
		// Most updates (resourceVersion, conditions, annotations) change
		// nothing visible; only log the ones that do.
		if len(details) > 0 {
			r.log(now, resources.Modified, e.Row, strings.Join(details, ", "))
		}
	}
	r.resort()
}

// logInitialProblems explains every pod already unhealthy at start.
func (r *Resource) logInitialProblems() {
	now := time.Now()
	for _, k := range r.order {
		p := r.rows[k].row
		if p.Health() == Healthy || p.Health() == Finished {
			continue
		}
		var details []string
		if msg := p.WaitMessage(); msg != "" {
			details = append(details, msg)
		}
		if p.Restarts > 0 {
			details = append(details, "restarts "+p.RestartsString())
		}
		if !p.LastExit.IsZero() {
			details = append(details, "last exit "+p.exitWho()+p.LastExit.Describe())
		}
		r.log(now, resources.State, p, strings.Join(details, ", "))
	}
}

// exitWho names the container of the last exit when there are several.
func (p Row) exitWho() string {
	if len(p.Containers) > 1 {
		return p.LastExitContainer + " "
	}
	return ""
}

func orNone(s string) string {
	if s == "" {
		return "<none>"
	}
	return s
}

func (r *Resource) applyMetrics(m Metrics) {
	if m.Err != nil {
		r.metricsErr = m.Err
		return
	}
	r.metricsErr = nil
	r.host.Pulse(m.At)
	r.usage, r.containerUsage = m.Usage, m.Containers
	for key, rs := range r.rows {
		u, ok := m.Usage[key]
		if !ok {
			continue
		}
		if rs.hasUsage {
			rs.cpuTrend = trend(rs.usage.CPU, u.CPU, 1)
			rs.memTrend = trend(rs.usage.Mem, u.Mem, 1<<20)
		}
		rs.usage, rs.hasUsage = u, true
	}
}

// trend compares in display units (m, Mi) so jitter below them doesn't flicker.
func trend(old, now, unit int64) int {
	switch {
	case old/unit == now/unit:
		return 0
	case now > old:
		return 1
	}
	return -1
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
