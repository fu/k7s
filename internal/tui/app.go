// Package tui draws k7s: one page for a namespace (and filter) with a
// section per kind and a change log underneath, the navigation bar, and the
// screens every kind's steps open (pickers, viewers, interactive programs).
//
// It knows nothing about specific kinds beyond the registry below: they say
// what to show (resources.Row, resources.Step), the tui says how.
package tui

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"k8s.io/cli-runtime/pkg/genericclioptions"

	"github.com/fu/k7s/internal/config"
	"github.com/fu/k7s/internal/kube"
	"github.com/fu/k7s/internal/resources"
	"github.com/fu/k7s/internal/resources/deployments"
	"github.com/fu/k7s/internal/resources/gitops"
	"github.com/fu/k7s/internal/resources/pods"
	"github.com/fu/k7s/internal/resources/replicasets"
	"github.com/fu/k7s/internal/resources/secrets"
)

// registry lists the kinds in display order. Each name's first letter
// toggles its section, so it must not clash with another key of the page.
func registry() []resources.Resource {
	return []resources.Resource{gitops.New(), deployments.New(), replicasets.New(), pods.New(), secrets.New()}
}

// Info describes what is being watched.
type Info struct {
	Context   string
	Server    string
	Namespace string
	Selector  string
	Refresh   time.Duration
	Notice    string // shown in the navigation bar at start
	// Config has the colors and the state to come back to (hidden
	// sections, last namespaces); changes are saved to it.
	Config *config.Config
}

// screen is what opens on top of the page: a picker or a viewer.
type screen interface {
	// update handles keys and the screen's own messages; done closes it.
	update(msg tea.Msg) (cmd tea.Cmd, done bool)
	// view renders a title line, an empty line and the body in width × height.
	view(width, height int) string
	hints() string
}

type model struct {
	ctx    context.Context
	flags  *genericclioptions.ConfigFlags // to build clients for other contexts
	client *kube.Client                   // the context in use
	send   func(tea.Msg)
	info   Info

	kinds   []resources.Resource
	screens []screen // the top one is shown
	page    page
	changes changeLog

	width, height int
	notice        string
	noticeAt      time.Time
	pulseAt       time.Time // when fresh metrics last arrived

	listed   []resources.Ref
	listedAt time.Time
}

// Run shows the page and blocks until the user quits.
func Run(ctx context.Context, flags *genericclioptions.ConfigFlags, client *kube.Client, info Info) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// k7s picks its own colors: don't ask the terminal for its background
	// (a terminal that never answers would stall the start for seconds).
	lipgloss.SetHasDarkBackground(true)
	if info.Config == nil {
		info.Config = &config.Config{} // nothing to restore, nothing saved
	}
	setPalette(info.Config.Colors)

	m := &model{ctx: ctx, flags: flags, client: client, info: info, kinds: registry()}
	m.page.cfg = info.Config
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithContext(ctx))
	m.send = p.Send
	if info.Notice != "" {
		m.Notice(info.Notice)
		m.noticeAt = time.Now().Add(4 * time.Second) // a little longer at start
	}
	if _, err := p.Run(); err != nil && !errors.Is(err, tea.ErrProgramKilled) {
		return err
	}
	return nil
}

// pulseMsg redraws the "watching" dot often enough for a smooth fade.
type pulseMsg struct{}

func pulseTick() tea.Cmd {
	return tea.Tick(80*time.Millisecond, func(time.Time) tea.Msg { return pulseMsg{} })
}

func (m *model) Init() tea.Cmd { return tea.Batch(pulseTick(), m.start()) }

// start (re)starts every kind and the event watch for the current scope.
func (m *model) start() tea.Cmd {
	m.changes.reset()
	m.page.reset()
	m.listedAt = time.Time{}
	m.watchEvents()
	var cmds []tea.Cmd
	for _, k := range m.kinds {
		cmds = append(cmds, k.Start(m))
	}
	return tea.Batch(cmds...)
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case pulseMsg:
		return m, pulseTick()
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		// The top screen gets every key; the page's keys work on the page.
		if top := m.top(); top != nil {
			cmd, done := top.update(msg)
			if done {
				m.close(top)
			}
			return m, cmd
		}
		return m, m.pageKey(msg)
	case execDoneMsg:
		if msg.err != nil {
			m.Notice(msg.err.Error())
		}
		return m, nil
	case eventMsg:
		m.addEvent(msg)
		return m, nil
	}

	// Everything else belongs to a kind or a screen; each ignores what is
	// not its own.
	var cmds []tea.Cmd
	for _, k := range m.kinds {
		cmds = append(cmds, k.Update(msg))
	}
	for _, s := range slices.Clone(m.screens) {
		cmd, done := s.update(msg)
		if done {
			m.close(s)
		}
		cmds = append(cmds, cmd)
	}
	return m, tea.Batch(cmds...)
}

// follow shows what a step leads to. opened reports whether a screen (or
// program) came up, so a picker that led to it stays underneath.
func (m *model) follow(step resources.Step) (cmd tea.Cmd, opened bool) {
	switch s := step.(type) {
	case *resources.Picker:
		return m.openPicker(s), true
	case *resources.Viewer:
		return m.openViewer(s), true
	case *resources.Exec:
		return runExec(s), true
	case resources.Cmd:
		return tea.Cmd(s), false
	}
	return nil, false
}

func (m *model) open(s screen) { m.screens = append(m.screens, s) }

func (m *model) top() screen {
	if len(m.screens) == 0 {
		return nil
	}
	return m.screens[len(m.screens)-1]
}

// close removes screen s (which may be covered by one it opened).
func (m *model) close(s screen) {
	if i := slices.Index(m.screens, s); i >= 0 {
		m.screens = slices.Delete(m.screens, i, i+1)
	}
}

// closeAll leaves every screen the way esc would, so streams stop.
func (m *model) closeAll() {
	for len(m.screens) > 0 {
		top := m.top()
		top.update(tea.KeyMsg{Type: tea.KeyEsc})
		m.close(top)
	}
}

// scopeChanged restarts everything for the new context, namespace or filter,
// and remembers the namespace for the context.
func (m *model) scopeChanged() tea.Cmd {
	m.closeAll()
	if c := m.info.Config; c != nil {
		c.SetNamespace(m.info.Context, m.info.Namespace)
		if err := c.Save(); err != nil {
			m.Notice("config: " + err.Error())
		}
	}
	return m.start()
}

func (m *model) View() string {
	if m.width == 0 {
		return "starting…"
	}
	if top := m.top(); top != nil {
		return fitLines(top.view(m.width, m.height-1), m.height-1) + "\n" + m.navBar(top.hints())
	}
	return m.pageView()
}

// fitLines pads or cuts s to exactly n lines.
func fitLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	for len(lines) < n {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

// ---- resources.Host -------------------------------------------------------------

var _ resources.Host = (*model)(nil)

func (m *model) Context() context.Context { return m.ctx }
func (m *model) Client() *kube.Client     { return m.client }
func (m *model) Namespace() string        { return m.info.Namespace }
func (m *model) Selector() string         { return m.info.Selector }
func (m *model) Refresh() time.Duration   { return m.info.Refresh }
func (m *model) Send(msg tea.Msg)         { m.send(msg) }
func (m *model) Log(c resources.Change)   { m.changes.add(c) }
func (m *model) Notice(s string)          { m.notice, m.noticeAt = s, time.Now() }
func (m *model) Pulse(at time.Time)       { m.pulseAt = at }

// Listed collects what all sections list. It is asked often while drawing
// (relevance of log entries), so it is cached briefly.
func (m *model) Listed() []resources.Ref {
	if time.Since(m.listedAt) > 200*time.Millisecond {
		m.listed = m.listed[:0]
		for _, k := range m.kinds {
			m.listed = append(m.listed, k.Listed()...)
		}
		m.listedAt = time.Now()
	}
	return m.listed
}

// ---- navigation bar --------------------------------------------------------------

// scopes are the menus that change what is shown: each opens a picker.
var scopes = []string{"contexts", "namespaces", "filter"}

// navBar: the scope menus (highlighted while their picker is open), the
// section toggles (highlighted while shown), then the commands of what is
// on screen — or a recent notice such as an error. Each menu is its name
// with the key letter (always the first) in cyan.
func (m *model) navBar(cmds string) string {
	item := func(name string, on bool) string {
		st, key := menu, menu.Foreground(infoColor).Bold(true)
		if on {
			st, key = menuActive, menuActive.Underline(true)
		}
		return st.Render(" ") + key.Render(name[:1]) + st.Render(name[1:]+" ") + " "
	}
	open := ""
	if p, ok := m.top().(*pickerScreen); ok {
		open = p.scope
	}
	var b strings.Builder
	for _, s := range scopes {
		b.WriteString(item(s, s == open))
	}
	b.WriteString(dim.Render("│ "))
	for _, k := range m.kinds {
		b.WriteString(item(k.Name(), m.page.shown(k)))
	}
	if m.notice != "" && time.Since(m.noticeAt) < 6*time.Second {
		cmds = yellow.Render(m.notice)
	}
	return clip(b.String()+" "+cmds, m.width)
}
