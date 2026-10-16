package commands

import (
	"fmt"
	"strconv"

	"github.com/fu/k7s/internal/resources"
	"github.com/fu/k7s/internal/resources/pods"
)

// Forward forwards one of the pod's declared ports to localhost (via a
// picker when it has several), like "kubectl port-forward"; again on a
// forwarded pod, it stops.
var Forward = resources.Command{
	Name: "forward",
	Run: func(t resources.Target) resources.Step {
		r, pod, ok := pod(t)
		switch {
		case !ok:
		case r.StopForwards(pod):
		case len(pod.Ports) == 0:
			t.Host.Notice("pod " + pod.Name + " declares no TCP ports to forward")
		case len(pod.Ports) == 1:
			r.StartForward(pod, pod.Ports[0])
		default:
			return portPicker(r, pod)
		}
		return nil
	},
}

func portPicker(r *pods.Resource, pod pods.Row) resources.Step {
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
			r.StartForward(pod, pod.Ports[i])
		}
		return nil
	}
	return p
}
