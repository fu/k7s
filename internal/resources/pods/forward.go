package pods

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/fu/k7s/internal/kube"
	"github.com/fu/k7s/internal/resources"
)

// Port-forwarding: w on a pod forwards one of its declared ports to
// localhost (via a picker when it has several); w again stops it. Forwards
// run in the background until stopped or k7s quits.

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

// toggleForward stops a pod's forwards, or starts one.
func (r *Resource) toggleForward(pod Row) resources.Step {
	stopped := false
	for key, f := range r.forwards {
		if f.pod.Key() == pod.Key() {
			f.cancel()
			delete(r.forwards, key)
			r.logForward(pod, fmt.Sprintf("stopped localhost:%d → %d", f.local, f.remote))
			stopped = true
		}
	}
	switch {
	case stopped:
	case len(pod.Ports) == 0:
		r.host.Notice("pod " + pod.Name + " declares no TCP ports to forward")
	case len(pod.Ports) == 1:
		r.startForward(pod, pod.Ports[0])
	default:
		return r.portPicker(pod)
	}
	return nil
}

func (r *Resource) portPicker(pod Row) resources.Step {
	p := &resources.Picker{
		Title:   "forward which port of " + pod.Name + "?",
		Columns: []resources.Column{{Title: "CONTAINER"}, {Title: "PORT", Right: true}, {Title: "NAME"}},
		Hint:    "forward",
	}
	for i, port := range pod.Ports {
		p.Rows = append(p.Rows, resources.Row{Key: strconv.Itoa(i), Cells: []resources.Cell{
			{Text: port.Container}, {Text: fmt.Sprint(port.Number)}, {Text: port.Name, Tone: resources.Dim},
		}})
	}
	p.Pick = func(key string) resources.Step {
		if i, err := strconv.Atoi(key); err == nil && i < len(pod.Ports) {
			r.startForward(pod, pod.Ports[i])
		}
		return nil
	}
	return p
}

func (r *Resource) logForward(pod Row, detail string) {
	r.host.Log(resources.Change{At: time.Now(), Kind: resources.Forward, Object: pod.Name, Detail: detail})
}

func (r *Resource) startForward(pod Row, p Port) {
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
