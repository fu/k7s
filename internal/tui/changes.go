package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/fu/k7s/internal/kube"
	"github.com/fu/k7s/internal/resources"
)

// The change log under the sections: what the kinds log, plus the
// Kubernetes events of the namespace (including the last hour or so from
// before k7s started).

const maxChanges = 1000

type changeLog struct {
	entries []resources.Change // oldest first
	back    int                // lines scrolled up
	gen     int                // of the event watch
	cancel  context.CancelFunc
}

func (c *changeLog) reset() { c.entries, c.back = nil, 0 }

// add keeps entries in time order: events from before the watch started
// carry their own, older timestamps.
func (c *changeLog) add(e resources.Change) {
	c.entries = append(c.entries, e)
	if n := len(c.entries); n > 1 && c.entries[n-2].At.After(e.At) {
		sort.SliceStable(c.entries, func(i, j int) bool { return c.entries[i].At.Before(c.entries[j].At) })
	}
	if len(c.entries) > maxChanges {
		c.entries = c.entries[len(c.entries)-maxChanges:]
	}
}

// view renders the newest entries that belong on the page in n lines; back
// scrolls up. Entries can span several lines (wrapping, extra items).
func (c *changeLog) view(n, width int) string {
	var shown []resources.Change
	for _, e := range c.entries {
		if e.Show == nil || e.Show() {
			shown = append(shown, e)
		}
	}
	// Wrap entries from the newest backwards, only as many as are needed.
	var all []string
	for i := len(shown) - 1; i >= 0 && len(all) < c.back+n; i-- {
		all = append(wrapChange(changeLine(shown[i]), width), all...)
	}
	c.back = max(0, min(c.back, len(all)-n))

	var b strings.Builder
	title := fmt.Sprintf("─ changes (%d) ", len(shown))
	if c.back > 0 {
		title += fmt.Sprintf("▲ scrolled up %d lines (a to go down) ", c.back)
	}
	b.WriteString(dim.Render(title + strings.Repeat("─", max(0, width-lipgloss.Width(title)))))
	b.WriteByte('\n')

	end := len(all) - c.back
	lines := all[max(0, end-n):end]
	if len(lines) == 0 {
		lines = append(lines, dim.Render("  no changes yet — what happens in this namespace appears here"))
	}
	for i := 0; i < n; i++ {
		if i < len(lines) {
			b.WriteString(lines[i])
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// changeLine formats an entry: time, kind, object, state, detail, and one
// extra line per item.
func changeLine(c resources.Change) string {
	k := fmt.Sprintf("%-8s", c.Kind)
	switch c.Kind {
	case resources.Added:
		k = green.Render(k)
	case resources.Deleted:
		k = red.Render(k)
	case resources.Warning:
		k = red.Bold(true).Render(k)
	case resources.Event:
		k = dim.Render(k)
	case resources.State, resources.Forward:
		k = cyan.Render(k)
	default:
		k = yellow.Render(k)
	}
	s := dim.Render(c.At.Format("15:04:05")) + " " + k + " " + c.Object
	if len(c.State) > 0 {
		s += "  " + cells(c.State)
	}
	if c.Detail != "" {
		if c.Kind == resources.Warning {
			s += "  " + yellow.Render(c.Detail)
		} else {
			s += dim.Render("  " + c.Detail)
		}
	}
	for _, it := range c.Items {
		s += "\n" + yellow.Render("· "+it)
	}
	return s
}

// changeIndent lines wrapped continuation lines up after the timestamp.
const changeIndent = 9

// wrapChange soft-wraps one entry. Its first line starts at the left edge;
// wrapped parts and extra lines are indented under the text.
func wrapChange(s string, width int) []string {
	var out []string
	for i, part := range strings.Split(s, "\n") {
		first := changeIndent
		if i == 0 {
			first = 0
		}
		out = append(out, wrap(part, width, first, changeIndent)...)
	}
	return out
}

// ---- Kubernetes events -----------------------------------------------------------

type eventMsg struct {
	gen int
	e   kube.Event
}

// watchEvents (re)starts the event watch of the namespace.
func (m *model) watchEvents() {
	c := &m.changes
	if c.cancel != nil {
		c.cancel()
	}
	c.gen++
	ctx, cancel := context.WithCancel(m.ctx)
	c.cancel = cancel
	gen, send := c.gen, m.send
	if err := m.client.WatchEvents(ctx, m.info.Namespace, func(e kube.Event) { send(eventMsg{gen, e}) }); err != nil {
		m.Notice("events: " + err.Error())
	}
}

// addEvent logs a Kubernetes event. With a filter it only shows while it is
// about something listed (decided when drawing: the lists may still be
// arriving).
func (m *model) addEvent(msg eventMsg) {
	e := msg.e
	if msg.gen != m.changes.gen || e.Reason == "BackOff" {
		return // BackOff is already visible as restarts / CrashLoopBackOff
	}
	c := resources.Change{At: e.At, Kind: resources.Event, Object: strings.ToLower(e.Kind) + "/" + e.Name, Detail: e.Reason + ": " + e.Message}
	if e.Type == "Warning" {
		c.Kind = resources.Warning
	}
	if e.Kind == "Pod" {
		c.Object = e.Name
	}
	if e.Count > 1 {
		c.Detail += fmt.Sprintf(" (×%d)", e.Count)
	}
	if m.info.Selector != "" {
		c.Show = func() bool { return m.aboutListed(e) }
	}
	m.changes.add(c)
}

// aboutListed reports whether an event is about a listed object, or
// belongs to a listed workload: its ReplicaSets, and its earlier pods that
// are gone by now (their history is what explains the current ones).
func (m *model) aboutListed(e kube.Event) bool {
	for _, ref := range m.Listed() {
		if ref.Namespace != e.Namespace {
			continue
		}
		if ref.Kind == e.Kind && ref.Name == e.Name {
			return true
		}
		owned := (e.Kind == "Pod" && ref.Kind != "Pod" && ref.Kind != "Secret") ||
			(e.Kind == "ReplicaSet" && ref.Kind == "Deployment")
		if owned && strings.HasPrefix(e.Name, ref.Name+"-") {
			return true
		}
	}
	return false
}
