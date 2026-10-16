package commands

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"

	corev1 "k8s.io/api/core/v1"

	"github.com/fu/k7s/internal/kube"
	"github.com/fu/k7s/internal/resources"
	"github.com/fu/k7s/internal/resources/pods"
)

const tailLines = 500

// Logs follows a container's logs like "kubectl logs -f --tail=500".
var Logs = resources.Command{
	Name: "logs",
	Run: func(t resources.Target) resources.Step {
		return pickContainer(t, "logs", false, func(pod pods.Row, c pods.Container) resources.Step {
			return logs(t.Host.Client(), pod, c.Name, false)
		})
	},
}

// PreviousLogs shows the logs of a container's previous run, like
// "kubectl logs -p": why it crashed before the restart.
var PreviousLogs = resources.Command{
	Name: "previous-logs",
	Run: func(t resources.Target) resources.Step {
		return pickContainer(t, "previous logs", false, func(pod pods.Row, c pods.Container) resources.Step {
			if c.Restarts == 0 {
				t.Host.Notice("container " + c.Name + " has not restarted: no previous logs")
				return nil
			}
			return logs(t.Host.Client(), pod, c.Name, true)
		})
	},
}

func logs(client *kube.Client, pod pods.Row, container string, previous bool) resources.Step {
	title := fmt.Sprintf("logs %s [%s]", pod.Name, container)
	if previous {
		title = "previous " + title
	}
	return &resources.Viewer{
		Title: title,
		Live:  !previous,
		Stream: func(ctx context.Context, emit func(resources.Line)) error {
			return streamLogs(ctx, client, pod.Namespace, pod.Name, container, previous,
				func(s string) { emit(resources.Line{Text: s}) })
		},
	}
}

// streamLogs calls line for every log line until the stream ends (nil
// error), fails, or ctx is done.
func streamLogs(ctx context.Context, c *kube.Client, ns, pod, container string, previous bool, line func(string)) error {
	tail := int64(tailLines)
	stream, err := c.Kube.CoreV1().Pods(ns).GetLogs(pod, &corev1.PodLogOptions{
		Container: container,
		Follow:    !previous,
		Previous:  previous,
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
