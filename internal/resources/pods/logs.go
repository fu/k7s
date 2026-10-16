package pods

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/remotecommand"

	"github.com/fu/k7s/internal/kube"
	"github.com/fu/k7s/internal/resources"
)

// Logs and shell need a container: straight away for a pod with one,
// otherwise via a picker.

const logTailLines = 500

// containers leads to logs (or a shell) of one of the pod's containers.
func (r *Resource) containers(pod Row, shell bool) resources.Step {
	var list []Container
	for _, c := range pod.Containers {
		// Any container has logs; a shell needs a long-running one.
		if !shell || c.Kind != KindInit {
			list = append(list, c)
		}
	}
	open := func(c Container) resources.Step {
		if shell {
			return r.shell(pod, c)
		}
		return r.logs(pod, c.Name)
	}
	switch len(list) {
	case 0:
		r.host.Notice("no containers to choose from")
		return nil
	case 1:
		return open(list[0])
	}

	p := &resources.Picker{
		Title: "container of " + pod.Name,
		Columns: []resources.Column{
			{Title: "CONTAINER"}, {Title: "TYPE"}, {Title: "READY"}, {Title: "STATE"}, {Title: "RESTARTS"},
			{Title: "CPU", Right: true}, {Title: "MEM", Right: true}, {Title: "IMAGE", Shrink: true},
		},
		Hint: "logs",
	}
	if shell {
		p.Hint = "shell"
	}
	usage := r.containerUsage[pod.Key()]
	for _, c := range list {
		if p.Current == "" && c.Kind == KindMain {
			p.Current = c.Name // rather than an init container
		}
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
		p.Rows = append(p.Rows, resources.Row{Key: c.Name, Cells: []resources.Cell{
			{Text: c.Name}, {Text: kind, Tone: resources.Dim}, ready, state, restarts,
			{Text: cpu}, {Text: mem}, {Text: c.Image, Tone: resources.Dim},
		}})
	}
	p.Pick = func(name string) resources.Step {
		for _, c := range list {
			if c.Name == name {
				return open(c)
			}
		}
		return nil
	}
	return p
}

// logs follows a container's logs like "kubectl logs -f --tail".
func (r *Resource) logs(pod Row, container string) resources.Step {
	client := r.host.Client()
	return &resources.Viewer{
		Title: fmt.Sprintf("logs %s [%s]", pod.Name, container),
		Live:  true,
		Stream: func(ctx context.Context, emit func(resources.Line)) error {
			return streamLogs(ctx, client, pod.Namespace, pod.Name, container, logTailLines,
				func(s string) { emit(resources.Line{Text: s}) })
		},
	}
}

// shell runs an interactive shell in a running container.
func (r *Resource) shell(pod Row, c Container) resources.Step {
	if c.State != "Running" {
		r.host.Notice(fmt.Sprintf("no shell: container %s is %s", c.Name, c.State))
		return nil
	}
	client := r.host.Client()
	return &resources.Exec{
		Banner: fmt.Sprintf("shell in %s/%s [%s] — type 'exit' to return to k7s", pod.Namespace, pod.Name, c.Name),
		Run: func(stdin io.Reader, stdout io.Writer, sizes remotecommand.TerminalSizeQueue) error {
			return client.Exec(context.Background(), pod.Namespace, pod.Name, c.Name, kube.ShellCommand, stdin, stdout, sizes)
		},
	}
}

// streamLogs calls line for every log line until the stream ends (nil
// error), fails, or ctx is done.
func streamLogs(ctx context.Context, c *kube.Client, ns, pod, container string, tail int64, line func(string)) error {
	stream, err := c.Kube.CoreV1().Pods(ns).GetLogs(pod, &corev1.PodLogOptions{
		Container: container,
		Follow:    true,
		TailLines: &tail,
	}).Stream(ctx)
	if err != nil {
		return err
	}
	defer stream.Close()
	rd := bufio.NewReaderSize(stream, 64*1024)
	for {
		s, err := rd.ReadString('\n')
		if s != "" {
			line(sanitize(s))
		}
		if err == io.EOF || ctx.Err() != nil {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// sanitize makes a log line safe to draw: no trailing newline, no carriage
// returns, tabs as spaces.
func sanitize(s string) string {
	s = strings.TrimRight(s, "\r\n")
	s = strings.ReplaceAll(s, "\r", "")
	return strings.ReplaceAll(s, "\t", "    ")
}
