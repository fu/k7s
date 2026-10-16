package tui

import (
	"context"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/fu/k7s/internal/namespaces"
	"github.com/fu/k7s/internal/pods"
)

type (
	nsLoadedMsg struct {
		gen   int
		items []namespaces.Summary
		err   error
	}
	nsRefreshMsg struct {
		gen int
	}
)

type nsState struct {
	gen            int // bumped whenever the screen is left, to stop refreshes
	items          []namespaces.Summary
	err            error
	loadedAt       time.Time
	placed         bool // cursor moved to the current namespace yet
	cursor, offset int
}

// openNamespaces lists the namespaces of the current context.
func (m *model) openNamespaces() tea.Cmd {
	ns := &m.namespaces
	*ns = nsState{gen: ns.gen + 1}
	m.screen = screenNamespaces
	return m.loadNamespaces()
}

func (m *model) loadNamespaces() tea.Cmd {
	gen, client, ctx := m.namespaces.gen, m.client, m.ctx
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		items, err := namespaces.List(ctx, client)
		return nsLoadedMsg{gen, items, err}
	}
}

func (m *model) namespacesMsg(msg tea.Msg) tea.Cmd {
	ns := &m.namespaces
	switch msg := msg.(type) {
	case nsLoadedMsg:
		if msg.gen != ns.gen {
			return nil
		}
		ns.err, ns.loadedAt = msg.err, time.Now()
		switch {
		case msg.err == nil:
			ns.items = msg.items
		case len(ns.items) == 0:
			// Listing namespaces may be forbidden; still offer the
			// context's own namespace so it can be used.
			ns.items = []namespaces.Summary{{Name: m.client.Namespace}}
		}
		if !ns.placed {
			ns.placed = true
			for i, it := range ns.items {
				if it.Name == m.info.Namespace {
					ns.cursor = i
				}
			}
		}
		gen := ns.gen
		return tea.Tick(m.info.Refresh, func(time.Time) tea.Msg { return nsRefreshMsg{gen} })
	case nsRefreshMsg:
		if msg.gen == ns.gen && m.screen == screenNamespaces {
			return m.loadNamespaces()
		}
	}
	return nil
}

func (m *model) namespacesKey(msg tea.KeyMsg) tea.Cmd {
	ns := &m.namespaces
	n := len(ns.items)
	switch msg.String() {
	case "esc":
		m.leave()
		m.screen = screenMain
	case "up", "k":
		ns.cursor = clampCursor(ns.cursor, -1, n)
	case "down", "j":
		ns.cursor = clampCursor(ns.cursor, 1, n)
	case "pgup":
		ns.cursor = clampCursor(ns.cursor, -(m.height - 4), n)
	case "pgdown":
		ns.cursor = clampCursor(ns.cursor, m.height-4, n)
	case "home":
		ns.cursor = 0
	case "end", "G":
		ns.cursor = n - 1
	case "enter":
		if ns.cursor < n {
			m.info.Namespace = ns.items[ns.cursor].Name
			m.scopeChanged()
		}
		m.leave()
		m.screen = screenMain
	}
	return nil
}

func (m *model) namespacesView() string {
	ns := &m.namespaces
	t := table{
		headers: []string{" ", "NAMESPACE", "STATUS", "PODS", "RUNNING", "PENDING", "FAILING", "CPU", "MEM", "AGE"},
		right:   []bool{false, false, false, true, true, true, true, true, true, false},
		flex:    1,
	}

	for _, it := range ns.items {
		t.rows = append(t.rows, m.nsRow(it, it.Name == m.info.Namespace))
	}

	state := stDim.Render("loading…")
	switch {
	case ns.err != nil:
		state = stRed.Render("error: " + ns.err.Error())
	case !ns.loadedAt.IsZero():
		state = stDim.Render(fmt.Sprintf("refreshed %ds ago", int(time.Since(ns.loadedAt).Seconds())))
	}
	head := m.crumbs(m.info.Context, stYellow.Render(fmt.Sprintf("namespaces (%d)", len(ns.items))), state)
	body := t.render(ns.cursor, &ns.offset, m.height-3, m.width)
	return head + "\n\n" + body + m.footer("[enter] switch  [esc] back")
}

func (m *model) nsRow(s namespaces.Summary, current bool) []cell {
	marker := cell{text: " "}
	if current {
		marker = cell{text: "●", style: stGreen}
	}
	status := cell{text: s.Status, style: stDim}
	if s.Status == "Terminating" {
		status.style = stMagenta
	}
	count := func(n int, st lipgloss.Style) cell {
		if !s.HasPods {
			return cell{text: "-", style: stDim}
		}
		if n == 0 {
			return cell{text: "0", style: stDim}
		}
		return cell{text: fmt.Sprint(n), style: st}
	}
	return []cell{
		marker,
		{text: s.Name},
		status,
		count(s.Pods, stHeader),
		count(s.Healthy, stGreen),
		count(s.Progressing, stAmber),
		count(s.Failing, stRed.Bold(true)),
		{text: dash(s.HasMetrics, pods.CPU(s.CPU))},
		{text: dash(s.HasMetrics, pods.Mem(s.Mem))},
		{text: pods.Age(s.Created), style: stDim},
	}
}
