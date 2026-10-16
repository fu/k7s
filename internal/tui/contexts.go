package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/fu/k7s/internal/kube"
	"github.com/fu/k7s/internal/resources"
)

// The scope menus open pickers like any kind's step does.

func (m *model) openScope(scope string, p *resources.Picker) tea.Cmd {
	cmd := m.openPicker(p)
	m.top().(*pickerScreen).scope = scope
	return cmd
}

// current marks the row of what is in use.
func current(on bool) resources.Cell {
	if on {
		return resources.Cell{Text: "●", Tone: resources.Good}
	}
	return resources.Cell{Text: " "}
}

// openContexts lists the kubeconfig contexts; switching lands on the
// context's default namespace.
func (m *model) openContexts() tea.Cmd {
	items, err := kube.Contexts(m.flags)
	if err != nil {
		m.Notice("contexts: " + err.Error())
		return nil
	}
	p := &resources.Picker{
		Title: fmt.Sprintf("contexts (%d)", len(items)),
		Columns: []resources.Column{
			{Title: " "}, {Title: "CONTEXT"}, {Title: "NAMESPACE"}, {Title: "SERVER", Shrink: true},
		},
		Current: m.info.Context,
		Hint:    "switch",
	}
	for _, c := range items {
		ns := resources.Cell{Text: c.Namespace}
		if ns.Text == "" {
			ns = resources.Cell{Text: "default", Tone: resources.Dim}
		}
		p.Rows = append(p.Rows, resources.Row{Key: c.Name, Cells: []resources.Cell{
			current(c.Name == m.info.Context), {Text: c.Name}, ns, {Text: c.Server, Tone: resources.Dim},
		}})
	}
	p.Pick = func(name string) resources.Step {
		if name == m.info.Context {
			return nil
		}
		c, err := kube.ForContext(m.flags, name)
		if err != nil {
			m.Notice("context " + name + ": " + err.Error())
			return nil
		}
		m.client = c
		m.info.Context, m.info.Server, m.info.Namespace = c.Context, c.Server, c.Namespace
		return resources.Cmd(m.scopeChanged())
	}
	return m.openScope("contexts", p)
}
