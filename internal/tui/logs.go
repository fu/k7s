package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/fu/k7s/internal/pods"
)

const (
	logTailLines = 500
	maxLogLines  = 10000
)

type (
	logLineMsg struct {
		gen  int
		line string
	}
	logEndMsg struct {
		gen int
		err error
	}
)

type logsState struct {
	gen       int
	cancel    context.CancelFunc
	pod       pods.Row
	container string
	back      screen // where esc returns to

	lines   []string
	follow  bool // stick to the newest line
	top     int  // first line shown when not following
	tailTop int  // first line shown when following (from the last render)
	ended   bool
	err     error
}

// openLogs streams the container's logs like "kubectl logs -f --tail".
func (m *model) openLogs(pod pods.Row, container string) {
	l := &m.logs
	if l.cancel != nil {
		l.cancel()
	}
	gen := l.gen + 1
	ctx, cancel := context.WithCancel(m.ctx)
	*l = logsState{gen: gen, cancel: cancel, pod: pod, container: container, back: m.screen, follow: true}
	m.screen = screenLogs

	send, client := m.send, m.client
	go func() {
		err := pods.StreamLogs(ctx, client, pod.Namespace, pod.Name, container, logTailLines,
			func(line string) { send(logLineMsg{gen, line}) })
		if ctx.Err() == nil {
			send(logEndMsg{gen, err})
		}
	}()
}

func (m *model) logsMsg(msg tea.Msg) {
	l := &m.logs
	switch msg := msg.(type) {
	case logLineMsg:
		if msg.gen != l.gen {
			return
		}
		l.lines = append(l.lines, msg.line)
		if over := len(l.lines) - maxLogLines; over > 0 {
			l.lines = l.lines[over:]
			l.top = max(0, l.top-over)
		}
	case logEndMsg:
		if msg.gen == l.gen {
			l.ended, l.err = true, msg.err
		}
	}
}

func (m *model) logsKey(msg tea.KeyMsg) tea.Cmd {
	l := &m.logs
	page := max(1, m.height-4)
	switch msg.String() {
	case "esc":
		l.cancel()
		l.gen++ // drop lines still in flight
		m.screen = l.back
	case "up", "k":
		l.scroll(-1)
	case "down", "j":
		l.scroll(1)
	case "pgup":
		l.scroll(-page)
	case "pgdown", " ":
		l.scroll(page)
	case "home":
		l.follow, l.top = false, 0
	case "end", "G", "f":
		l.follow = true
	}
	return nil
}

func (l *logsState) scroll(delta int) {
	if l.follow {
		l.follow, l.top = false, l.tailTop
	}
	l.top = max(0, min(len(l.lines)-1, l.top+delta))
	if delta > 0 && l.top >= l.tailTop {
		l.follow = true
	}
}

// wrapLine soft-wraps one log line to the terminal width.
func wrapLine(s string, w int) []string {
	if lipgloss.Width(s) <= w {
		return []string{s}
	}
	return strings.Split(lipgloss.NewStyle().Width(w).Render(s), "\n")
}

func (m *model) logsView() string {
	l := &m.logs
	bodyH := max(1, m.height-3)

	// Where the view starts when following: fill the body from the bottom.
	tailTop, used := len(l.lines), 0
	for tailTop > 0 {
		n := len(wrapLine(l.lines[tailTop-1], m.width))
		if used+n > bodyH && used > 0 {
			break
		}
		used += n
		tailTop--
	}
	l.tailTop = tailTop

	start := l.top
	if l.follow {
		start = tailTop
	}
	var out []string
	for i := start; i < len(l.lines) && len(out) < bodyH; i++ {
		out = append(out, wrapLine(l.lines[i], m.width)...)
	}
	switch {
	case l.err != nil:
		out = append(out, "", stRed.Render("error: "+l.err.Error()))
	case len(l.lines) == 0 && !l.ended:
		out = append(out, stDim.Render("waiting for logs…"))
	case len(l.lines) == 0:
		out = append(out, stDim.Render("no logs"))
	}
	if len(out) > bodyH {
		out = out[len(out)-bodyH:]
	}
	for len(out) < bodyH {
		out = append(out, "")
	}

	state := stGreen.Render("● following")
	switch {
	case l.ended:
		state = stDim.Render("■ stream ended")
	case !l.follow:
		state = stYellow.Render("❚❚ paused — G to follow")
	}
	head := m.crumbs(m.info.Context, state, stDim.Render(fmt.Sprintf("%d lines", len(l.lines))),
		"container "+stCyan.Render(l.container), "pod "+stCyan.Render(l.pod.Name), "ns "+stCyan.Render(l.pod.Namespace))
	return head + "\n\n" + strings.Join(out, "\n") + "\n" +
		m.footer("[f]ollow  [esc] back")
}
