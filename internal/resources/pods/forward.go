package pods

import (
	"context"
	"fmt"
	"sort"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/fu/k7s/internal/kube"
	"github.com/fu/k7s/internal/resources"
)

// The port-forwards of pods: they run in the background until stopped or
// k7s quits, show on their pod's row (⇄:port) and in the change log. The
// forward command (commands/forward.go) starts and stops them.

type (
	fwdReadyMsg struct{ key string }
	fwdEndMsg   struct {
		key string
		err error
	}
)

type forward struct {
	pod           Row
	remote, local int
	cancel        context.CancelFunc
	ready         bool
}

// StopForwards ends a pod's forwards; it reports whether there were any.
func (r *Resource) StopForwards(pod Row) bool {
	stopped := false
	for key, f := range r.forwards {
		if f.pod.Key() == pod.Key() {
			f.cancel()
			delete(r.forwards, key)
			r.logForward(pod, fmt.Sprintf("stopped localhost:%d → %d", f.local, f.remote))
			stopped = true
		}
	}
	return stopped
}

func (r *Resource) logForward(pod Row, detail string) {
	r.host.Log(resources.Change{At: time.Now(), Kind: resources.Forward, Object: pod.Name, Detail: detail})
}

// StartForward forwards a free local port (the same number if free) to the
// pod's port.
func (r *Resource) StartForward(pod Row, p Port) {
	local, err := kube.FreeLocalPort(p.Number)
	if err != nil {
		r.host.Notice("no free local port: " + err.Error())
		return
	}
	key := fmt.Sprintf("%s/%d", pod.Key(), p.Number)
	ctx, cancel := context.WithCancel(r.host.Context())
	r.forwards[key] = &forward{pod: pod, remote: p.Number, local: local, cancel: cancel}

	ready := make(chan struct{})
	client, send := r.host.Client(), r.host.Send
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

func (r *Resource) forwardMsg(msg tea.Msg) {
	switch msg := msg.(type) {
	case fwdReadyMsg:
		if f, ok := r.forwards[msg.key]; ok {
			f.ready = true
			r.logForward(f.pod, fmt.Sprintf("localhost:%d → %d", f.local, f.remote))
		}
	case fwdEndMsg:
		f, ok := r.forwards[msg.key]
		if !ok {
			return // stopped on purpose
		}
		delete(r.forwards, msg.key)
		why := "ended"
		if msg.err != nil {
			why = "failed: " + msg.err.Error()
		}
		r.logForward(f.pod, fmt.Sprintf("localhost:%d → %d %s", f.local, f.remote, why))
		r.host.Notice(fmt.Sprintf("forward to %s:%d %s", f.pod.Name, f.remote, why))
	}
}

// forwardsOf returns the local ports forwarded to a pod, in order.
func (r *Resource) forwardsOf(podKey string) []int {
	var locals []int
	for _, f := range r.forwards {
		if f.pod.Key() == podKey && f.ready {
			locals = append(locals, f.local)
		}
	}
	sort.Ints(locals)
	return locals
}
