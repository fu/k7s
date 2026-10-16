package pods

import (
	"bufio"
	"context"
	"io"
	"strings"

	corev1 "k8s.io/api/core/v1"

	"github.com/fu/k7s/internal/kube"
)

// StreamLogs follows a container's logs like "kubectl logs -f --tail=tail",
// calling line for every line. It blocks until the stream ends (nil error),
// fails, or ctx is done.
func StreamLogs(ctx context.Context, c *kube.Client, ns, pod, container string, tail int64, line func(string)) error {
	stream, err := c.Kube.CoreV1().Pods(ns).GetLogs(pod, &corev1.PodLogOptions{
		Container: container,
		Follow:    true,
		TailLines: &tail,
	}).Stream(ctx)
	if err != nil {
		return err
	}
	defer stream.Close()
	r := bufio.NewReaderSize(stream, 64*1024)
	for {
		s, err := r.ReadString('\n')
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
