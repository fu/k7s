package tui

import (
	"context"
	"fmt"
	"io"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/fu/k7s/internal/kube"
	"github.com/fu/k7s/internal/resources"
)

// Print shows every section once, the same rows as the page, and returns
// (k7s --once, or when stdout is not a terminal).
func Print(ctx context.Context, client *kube.Client, info Info, w io.Writer) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	h := &onceHost{ctx: ctx, client: client, info: info, msgs: make(chan tea.Msg, 1024), kinds: ordered(info.Config, registry())}
	for _, k := range h.kinds {
		h.run(k.Start(h))
	}

	// Feed the kinds their messages until all have their first list, plus
	// one metrics refresh so CPU/MEM are filled in.
	var readyAt time.Time
	for {
		ready := true
		for _, k := range h.kinds {
			ready = ready && k.Ready()
		}
		if ready && readyAt.IsZero() {
			readyAt = time.Now()
		}
		if !readyAt.IsZero() && (h.pulsed || time.Since(readyAt) > info.Refresh+time.Second) {
			break
		}
		select {
		case msg := <-h.msgs:
			if batch, ok := msg.(tea.BatchMsg); ok {
				for _, c := range batch {
					h.run(c)
				}
				continue
			}
			for _, k := range h.kinds {
				h.run(k.Update(msg))
			}
		case <-time.After(100 * time.Millisecond):
		case <-ctx.Done():
			return fmt.Errorf("timed out waiting for the lists")
		}
	}

	fmt.Fprintf(w, "ctx %s │ ns %s\n", info.Context, info.Namespace)
	for _, k := range h.kinds {
		if !k.Available() || (info.Config != nil && info.Config.Hidden(k.Name())) {
			continue
		}
		var rows []resources.Row
		for _, r := range k.Rows() {
			if !info.Config.StateHidden(k.Name(), r.State) {
				rows = append(rows, r)
			}
		}
		fmt.Fprintf(w, "\n%s\n", k.Name())
		if len(rows) == 0 {
			fmt.Fprintf(w, "  %s\n", k.Empty())
			continue
		}
		header, lines := table{cols: k.Columns(), rows: rows, number: true}.lines("", 1<<16)
		fmt.Fprintln(w, header)
		for _, l := range lines {
			fmt.Fprintln(w, l)
		}
	}
	return nil
}

// onceHost runs the kinds without a screen.
type onceHost struct {
	ctx    context.Context
	client *kube.Client
	info   Info
	msgs   chan tea.Msg
	kinds  []resources.Resource
	pulsed bool
}

// run executes a command in the background, delivering its message.
func (h *onceHost) run(cmd tea.Cmd) {
	if cmd != nil {
		go func() {
			if msg := cmd(); msg != nil {
				h.Send(msg)
			}
		}()
	}
}

func (h *onceHost) Context() context.Context { return h.ctx }
func (h *onceHost) Client() *kube.Client     { return h.client }
func (h *onceHost) Namespace() string        { return h.info.Namespace }
func (h *onceHost) Selector() string         { return h.info.Selector }
func (h *onceHost) Refresh() time.Duration   { return h.info.Refresh }
func (h *onceHost) Log(resources.Change)     {}
func (h *onceHost) Notice(string)            {}
func (h *onceHost) Pulse(time.Time)          { h.pulsed = true }

func (h *onceHost) Send(msg tea.Msg) {
	select {
	case h.msgs <- msg:
	case <-h.ctx.Done():
	}
}

func (h *onceHost) Listed() []resources.Ref {
	var out []resources.Ref
	for _, k := range h.kinds {
		out = append(out, k.Listed()...)
	}
	return out
}
