package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/fu/k7s/internal/pods"
)

type purpose int

const (
	forLogs purpose = iota
	forShell
)

type containersState struct {
	pod            pods.Row
	purpose        purpose
	cursor, offset int
}

// candidates lists the containers usable for p: any container has logs,
// but a shell needs a long-running (main or sidecar) container.
func candidates(r pods.Row, p purpose) []pods.Container {
	var out []pods.Container
	for _, c := range r.Containers {
		if p == forShell && c.Kind == pods.KindInit {
			continue
		}
		out = append(out, c)
	}
	return out
}

// openContainers acts on the selected pod: straight to logs/shell for a
// single container, otherwise via the container picker.
func (m *model) openContainers(p purpose) tea.Cmd {
	rs := m.pods.selectedRow()
	if rs == nil {
		return nil
	}
	if !rs.deletedAt.IsZero() {
		m.setNotice("pod " + rs.row.Name + " is gone")
		return nil
	}
	list := candidates(rs.row, p)
	if len(list) == 0 {
		m.setNotice("no containers to choose from")
		return nil
	}
	m.containers = containersState{pod: rs.row, purpose: p}
	if len(list) == 1 {
		return m.runOn(list[0], p)
	}
	// Preselect the first main container rather than an init container.
	for i, c := range list {
		if c.Kind == pods.KindMain {
			m.containers.cursor = i
			break
		}
	}
	m.screen = screenContainers
	return nil
}

// currentPod returns the live version of the pod the picker was opened for.
func (m *model) currentPod() pods.Row {
	if rs, ok := m.pods.rows[m.containers.pod.Key()]; ok {
		return rs.row
	}
	return m.containers.pod
}

func (m *model) runOn(c pods.Container, p purpose) tea.Cmd {
	pod := m.currentPod()
	if p == forLogs {
		m.openLogs(pod, c.Name)
		return nil
	}
	if c.State != "Running" {
		m.setNotice(fmt.Sprintf("no shell: container %s is %s", c.Name, c.State))
		return nil
	}
	return tea.Exec(&shellCmd{client: m.client, ns: pod.Namespace, pod: pod.Name, container: c.Name},
		func(err error) tea.Msg { return shellDoneMsg{err} })
}

func (m *model) containersKey(msg tea.KeyMsg) tea.Cmd {
	cs := &m.containers
	list := candidates(m.currentPod(), cs.purpose)
	switch msg.String() {
	case "esc":
		m.screen = screenPods
	case "up", "k":
		cs.cursor = clampCursor(cs.cursor, -1, len(list))
	case "down", "j":
		cs.cursor = clampCursor(cs.cursor, 1, len(list))
	case "enter":
		if cs.cursor < len(list) {
			return m.runOn(list[cs.cursor], cs.purpose)
		}
	}
	return nil
}

func (m *model) containersView() string {
	cs := &m.containers
	pod := m.currentPod()
	list := candidates(pod, cs.purpose)
	cs.cursor = clampCursor(cs.cursor, 0, len(list))
	action := "logs"
	if cs.purpose == forShell {
		action = "shell"
	}

	usage := m.pods.containerUsage[pod.Key()]
	t := table{
		headers: []string{"CONTAINER", "TYPE", "READY", "STATE", "RESTARTS", "CPU", "MEM", "IMAGE"},
		right:   []bool{false, false, false, false, false, true, true, false},
		flex:    7,
	}
	for _, c := range list {
		kind := string(c.Kind)
		if kind == "" {
			kind = "main"
		}
		ready := cell{text: "false", style: stYellow}
		if c.Ready {
			ready = cell{text: "true", style: stGreen}
		}
		state := cell{text: c.State, style: stRed.Bold(true)}
		switch c.State {
		case "Running":
			state.style = stGreen
		case "Completed":
			state.style = stDim
		case "Waiting", "ContainerCreating", "PodInitializing":
			state.style = stYellow
		}
		restarts := cell{text: fmt.Sprint(c.Restarts)}
		if c.Restarts > 0 {
			restarts.style = stYellow
		}
		u, ok := usage[c.Name]
		t.rows = append(t.rows, []cell{
			{text: c.Name},
			{text: kind, style: stDim},
			ready,
			state,
			restarts,
			{text: dash(ok, pods.CPU(u.CPU))},
			{text: dash(ok, pods.Mem(u.Mem))},
			{text: c.Image, style: stDim},
		})
	}

	head := m.crumbs(m.info.Context, stYellow.Render("select a container for "+action),
		"pod "+stCyan.Render(pod.Name), "ns "+stCyan.Render(pod.Namespace))
	body := t.render(cs.cursor, &cs.offset, m.height-3, m.width)
	return head + "\n\n" + body + m.footer(fmt.Sprintf("[enter] %s  [esc] back", action))
}
