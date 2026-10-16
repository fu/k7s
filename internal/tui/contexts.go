package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/fu/k7s/internal/kube"
)

type ctxState struct {
	items          []kube.ContextInfo
	err            error
	cursor, offset int
}

// openContexts lists the kubeconfig contexts, starting on the one in use.
func (m *model) openContexts() tea.Cmd {
	items, err := kube.Contexts(m.flags)
	m.contexts = ctxState{items: items, err: err}
	for i, it := range items {
		if it.Name == m.info.Context {
			m.contexts.cursor = i
		}
	}
	m.screen = screenContexts
	return nil
}

func (m *model) contextsKey(msg tea.KeyMsg) tea.Cmd {
	cs := &m.contexts
	n := len(cs.items)
	switch msg.String() {
	case "esc":
		m.screen = screenMain
	case "up", "k":
		cs.cursor = clampCursor(cs.cursor, -1, n)
	case "down", "j":
		cs.cursor = clampCursor(cs.cursor, 1, n)
	case "pgup":
		cs.cursor = clampCursor(cs.cursor, -(m.height - 4), n)
	case "pgdown":
		cs.cursor = clampCursor(cs.cursor, m.height-4, n)
	case "home":
		cs.cursor = 0
	case "end", "G":
		cs.cursor = n - 1
	case "enter":
		if cs.cursor >= n {
			return nil
		}
		name := cs.items[cs.cursor].Name
		m.screen = screenMain
		if name == m.info.Context {
			return nil
		}
		c, err := kube.ForContext(m.flags, name)
		if err != nil {
			m.setNotice("context " + name + ": " + err.Error())
			return nil
		}
		// Switch to the context and its default namespace.
		m.client = c
		m.info.Context, m.info.Server, m.info.Namespace = c.Context, c.Server, c.Namespace
		m.scopeChanged()
		return m.startGitOps()
	}
	return nil
}

func (m *model) contextsView() string {
	cs := &m.contexts
	t := table{
		headers: []string{" ", "CONTEXT", "NAMESPACE", "SERVER"},
		right:   []bool{false, false, false, false},
		flex:    3,
	}
	for _, c := range cs.items {
		marker := cell{text: " "}
		if c.Name == m.info.Context {
			marker = cell{text: "●", style: stGreen}
		}
		ns := cell{text: c.Namespace}
		if ns.text == "" {
			ns = cell{text: "default", style: stDim}
		}
		t.rows = append(t.rows, []cell{
			marker,
			{text: c.Name},
			ns,
			{text: c.Server, style: stDim},
		})
	}

	state := ""
	if cs.err != nil {
		state = stRed.Render("error: " + cs.err.Error())
	}
	head := m.crumbs(m.info.Context, stYellow.Render(fmt.Sprintf("contexts (%d)", len(cs.items))), state)
	body := t.render(cs.cursor, &cs.offset, m.height-3, m.width)
	return head + "\n\n" + body + m.footer("[enter] switch  [esc] back")
}
