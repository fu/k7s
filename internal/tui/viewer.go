package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/fu/k7s/internal/resources"
)

// The viewer screen: text from a Viewer step, streamed (logs: following the
// newest line until scrolled, like "kubectl logs -f") or fetched once (a
// secret: values masked until revealed with r).

const maxViewerLines = 10000

type (
	viewerLineMsg struct {
		s    *viewerScreen
		line resources.Line
	}
	viewerEndMsg struct {
		s   *viewerScreen
		err error
	}
)

type viewerScreen struct {
	v       *resources.Viewer
	cancel  context.CancelFunc
	lines   []resources.Line
	secrets bool // some lines are masked
	reveal  bool
	follow  bool // stick to the newest line
	top     int  // first line shown when not following
	tailTop int  // first line shown when following (from the last render)
	page    int
	ended   bool
	err     error
}

func (m *model) openViewer(v *resources.Viewer) tea.Cmd {
	ctx, cancel := context.WithCancel(m.ctx)
	s := &viewerScreen{v: v, cancel: cancel, follow: v.Live, page: 10}
	m.open(s)
	send := m.send
	go func() {
		err := v.Stream(ctx, func(l resources.Line) { send(viewerLineMsg{s, l}) })
		if ctx.Err() == nil {
			send(viewerEndMsg{s, err})
		}
	}()
	return nil
}

func (s *viewerScreen) hints() string {
	h := "[esc] back"
	if s.secrets {
		if s.reveal {
			h = "hide [r]  " + h
		} else {
			h = "[r]eveal  " + h
		}
	}
	if s.v.Live {
		h = "[f]ollow  " + h
	}
	return hints(h)
}

func (s *viewerScreen) update(msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case viewerLineMsg:
		if msg.s == s {
			s.lines = append(s.lines, msg.line)
			s.secrets = s.secrets || msg.line.Secret
			if over := len(s.lines) - maxViewerLines; over > 0 {
				s.lines = s.lines[over:]
				s.top = max(0, s.top-over)
			}
		}
	case viewerEndMsg:
		if msg.s == s {
			s.ended, s.err = true, msg.err
		}
	case tea.KeyMsg:
		switch msg.String() {
		case "esc":
			s.cancel()
			return nil, true
		case "r":
			s.reveal = !s.reveal
		case "up", "k":
			s.scroll(-1)
		case "down", "j":
			s.scroll(1)
		case "pgup":
			s.scroll(-s.page)
		case "pgdown", " ":
			s.scroll(s.page)
		case "home":
			s.follow, s.top = false, 0
		case "end", "G", "f":
			s.follow = s.v.Live
		}
	}
	return nil, false
}

func (s *viewerScreen) scroll(delta int) {
	if s.follow {
		s.follow, s.top = false, s.tailTop
	}
	s.top = max(0, min(len(s.lines)-1, s.top+delta))
	if delta > 0 && s.top >= s.tailTop {
		s.follow = s.v.Live
	}
}

// wrapLine renders one line to screen lines: masked, split, wrapped.
func (s *viewerScreen) wrapLine(l resources.Line, width int) []string {
	text := l.Text
	if l.Secret && !s.reveal {
		text = "••••••••"
	}
	var out []string
	for _, part := range strings.Split(text, "\n") {
		for _, w := range wrap(part, width, l.Indent, l.Indent) {
			out = append(out, style(l.Tone).Render(w))
		}
	}
	return out
}

func (s *viewerScreen) view(width, height int) string {
	bodyH := max(1, height-2)
	s.page = max(1, bodyH-1)

	// Where the view starts when following: fill the body from the bottom.
	tailTop, used := len(s.lines), 0
	for tailTop > 0 {
		n := len(s.wrapLine(s.lines[tailTop-1], width))
		if used+n > bodyH && used > 0 {
			break
		}
		used += n
		tailTop--
	}
	s.tailTop = tailTop

	start := s.top
	if s.follow {
		start = tailTop
	}
	var out []string
	for i := start; i < len(s.lines) && len(out) < bodyH; i++ {
		out = append(out, s.wrapLine(s.lines[i], width)...)
	}
	switch {
	case s.err != nil:
		out = append(out, "", red.Render("error: "+s.err.Error()))
	case len(s.lines) == 0 && !s.ended:
		out = append(out, dim.Render("waiting…"))
	case len(s.lines) == 0:
		out = append(out, dim.Render("nothing to show"))
	}
	if s.follow && len(out) > bodyH {
		out = out[len(out)-bodyH:]
	}

	parts := []string{yellow.Render(s.v.Title)}
	if s.v.Live {
		state := green.Render("● following")
		switch {
		case s.ended:
			state = dim.Render("■ stream ended")
		case !s.follow:
			state = yellow.Render("❚❚ paused — G to follow")
		}
		parts = append(parts, state, dim.Render(fmt.Sprintf("%d lines", len(s.lines))))
	}
	return clip(joinParts(parts), width) + "\n\n" + strings.Join(out, "\n")
}
