package commands

import (
	"fmt"

	"github.com/fu/k7s/internal/kube"
	"github.com/fu/k7s/internal/resources"
	"github.com/fu/k7s/internal/resources/pods"
)

// pickContainer leads to open(container): straight away for a pod with one
// suitable container, otherwise via a picker (starting on the first main
// container). Long-running only leaves out init containers (for a shell).
func pickContainer(t resources.Target, what string, longRunning bool, open func(pods.Row, pods.Container) resources.Step) resources.Step {
	r, pod, ok := pod(t)
	if !ok {
		return nil
	}
	return choose(t.Host, pod, r.ContainerUsage(pod.Key()), what, longRunning, open)
}

func choose(h resources.Host, pod pods.Row, usage map[string]pods.Usage, what string, longRunning bool, open func(pods.Row, pods.Container) resources.Step) resources.Step {
	var list []pods.Container
	for _, c := range pod.Containers {
		if !longRunning || c.Kind != pods.KindInit {
			list = append(list, c)
		}
	}
	switch len(list) {
	case 0:
		h.Notice("no containers to choose from")
		return nil
	case 1:
		return open(pod, list[0])
	}

	p := &resources.Picker{
		Title: "container of " + pod.Name,
		Columns: []resources.Column{
			{Title: "CONTAINER"}, {Title: "TYPE"}, {Title: "READY"}, {Title: "STATE"}, {Title: "RESTARTS"},
			{Title: "CPU", Right: true}, {Title: "MEM", Right: true}, {Title: "IMAGE", Shrink: true},
		},
		Hint: what,
	}
	for _, c := range list {
		if p.Current == "" && c.Kind == pods.KindMain {
			p.Current = c.Name
		}
		p.Rows = append(p.Rows, resources.Row{Key: c.Name, Cells: containerCells(c, usage)})
	}
	p.Pick = func(name string) resources.Step {
		for _, c := range list {
			if c.Name == name {
				return open(pod, c)
			}
		}
		return nil
	}
	return p
}

func containerCells(c pods.Container, usage map[string]pods.Usage) []resources.Cell {
	kind := string(c.Kind)
	if kind == "" {
		kind = "main"
	}
	ready := resources.Cell{Text: "false", Tone: resources.Warn}
	if c.Ready {
		ready = resources.Cell{Text: "true", Tone: resources.Good}
	}
	state := resources.Cell{Text: c.State, Tone: resources.Alarm}
	switch c.State {
	case "Running":
		state.Tone = resources.Good
	case "Completed":
		state.Tone = resources.Dim
	case "Waiting", "ContainerCreating", "PodInitializing":
		state.Tone = resources.Warn
	}
	restarts := resources.Cell{Text: fmt.Sprint(c.Restarts)}
	if c.Restarts > 0 {
		restarts.Tone = resources.Warn
	}
	cpu, mem := "-", "-"
	if u, ok := usage[c.Name]; ok {
		cpu, mem = kube.CPU(u.CPU), kube.Mem(u.Mem)
	}
	return []resources.Cell{
		{Text: c.Name}, {Text: kind, Tone: resources.Dim}, ready, state, restarts,
		{Text: cpu}, {Text: mem}, {Text: c.Image, Tone: resources.Dim},
	}
}
