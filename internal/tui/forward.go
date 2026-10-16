package tui

import (
	"context"
	"fmt"
	"sort"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/fu/k7s/internal/kube"
	"github.com/fu/k7s/internal/pods"
)

// Port-forwarding: f on a pod forwards one of its declared ports to
// localhost (via a picker when it has several); f again stops it. Forwards
// run in the background until stopped or k7s quits.

const forwardKind pods.EventType = "FORWARD"

type (
	fwdReadyMsg struct{ key string }
	fwdEndMsg   struct {
		key string
		err error
	}
)

type forward struct {
	pod           pods.Row
	remote, local int
	cancel        context.CancelFunc
	ready         bool
}

type portsState struct {
	pod            pods.Row
	back           screen // where the picker returns to
	cursor, offset int
}

// toggleForward acts on the pod selected on the pods screen.
func (m *model) toggleForward() tea.Cmd {
	rs := m.pods.selectedRow()
	if rs == nil {
		return nil
	}
	if !rs.deletedAt.IsZero() && len(m.forwardsOf(rs.row.Key())) == 0 {
		m.setNotice("pod " + rs.row.Name + " is gone")
		return nil
	}
	return m.toggleForwardFor(rs.row)
}

// toggleForwardFor stops a pod's forwards, or starts one.
func (m *model) toggleForwardFor(pod pods.Row) tea.Cmd {
	stopped := false
	for key, f := range m.forwards {
		if f.pod.Key() == pod.Key() {
			f.cancel()
			delete(m.forwards, key)
			m.logForward(pod, fmt.Sprintf("stopped localhost:%d → %d", f.local, f.remote))
			stopped = true
		}
	}
	switch {
	case stopped:
	case len(pod.Ports) == 0:
		m.setNotice("pod " + pod.Name + " declares no TCP ports to forward")
	case len(pod.Ports) == 1:
		m.startForward(pod, pod.Ports[0])
	default:
		m.ports = portsState{pod: pod, back: m.screen}
		m.screen = screenPorts
	}
	return nil
}

// logForward records a forward start/stop in the change log.
func (m *model) logForward(pod pods.Row, detail string) {
	m.pods.logEvent(eventLine{at: time.Now(), kind: forwardKind, row: pod, detail: detail})
}

func (m *model) startForward(pod pods.Row, p pods.Port) {
	local, err := kube.FreeLocalPort(p.Number)
	if err != nil {
		m.setNotice("no free local port: " + err.Error())
		return
	}
	key := fmt.Sprintf("%s/%d", pod.Key(), p.Number)
	ctx, cancel := context.WithCancel(m.ctx)
	m.forwards[key] = &forward{pod: pod, remote: p.Number, local: local, cancel: cancel}

	ready := make(chan struct{})
	client, send := m.client, m.send
	go func() {
		select {
		case <-ready:
			send(fwdReadyMsg{key})
		case <-ctx.Done():
		}
	}()
	go func() {
		err := client.PortForward(ctx, pod.Namespace, pod.Name, local, p.Number, ready)
		send(fwdEndMsg{key, err})
	}()
}

func (m *model) forwardMsg(msg tea.Msg) {
	switch msg := msg.(type) {
	case fwdReadyMsg:
		if f, ok := m.forwards[msg.key]; ok {
			f.ready = true
			m.logForward(f.pod, fmt.Sprintf("localhost:%d → %d", f.local, f.remote))
		}
	case fwdEndMsg:
		f, ok := m.forwards[msg.key]
		if !ok {
			return // stopped on purpose
		}
		delete(m.forwards, msg.key)
		why := "ended"
		if msg.err != nil {
			why = "failed: " + msg.err.Error()
		}
		m.logForward(f.pod, fmt.Sprintf("localhost:%d → %d %s", f.local, f.remote, why))
		m.setNotice(fmt.Sprintf("forward to %s:%d %s", f.pod.Name, f.remote, why))
	}
}

// forwardsOf returns the local ports forwarded to a pod, in order.
func (m *model) forwardsOf(podKey string) []int {
	var locals []int
	for _, f := range m.forwards {
		if f.pod.Key() == podKey && f.ready {
			locals = append(locals, f.local)
		}
	}
	sort.Ints(locals)
	return locals
}

func (m *model) portsKey(msg tea.KeyMsg) tea.Cmd {
	ps := &m.ports
	switch msg.String() {
	case "esc":
		m.screen = ps.back
	case "up", "k":
		ps.cursor = clampCursor(ps.cursor, -1, len(ps.pod.Ports))
	case "down", "j":
		ps.cursor = clampCursor(ps.cursor, 1, len(ps.pod.Ports))
	case "enter":
		if ps.cursor < len(ps.pod.Ports) {
			m.startForward(ps.pod, ps.pod.Ports[ps.cursor])
		}
		m.screen = ps.back
	}
	return nil
}

func (m *model) portsView() string {
	ps := &m.ports
	t := table{
		headers: []string{"CONTAINER", "PORT", "NAME"},
		right:   []bool{false, true, false},
		flex:    0,
	}
	for _, p := range ps.pod.Ports {
		t.rows = append(t.rows, []cell{{text: p.Container}, {text: fmt.Sprint(p.Number)}, {text: p.Name, style: stDim}})
	}
	head := m.crumbs(m.info.Context, stYellow.Render("forward which port?"), "pod "+stCyan.Render(ps.pod.Name))
	body := t.render(ps.cursor, &ps.offset, m.height-3, m.width)
	return head + "\n\n" + body + m.footer("[enter] forward  [esc] back")
}
