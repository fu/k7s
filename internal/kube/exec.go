package kube

import (
	"context"
	"io"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/httpstream"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/remotecommand"
)

// ShellCommand starts bash when the image has it and falls back to sh.
var ShellCommand = []string{"sh", "-c", "command -v bash >/dev/null 2>&1 && exec bash || exec sh"}

// Exec runs cmd in a container with a TTY attached to stdin/stdout, the same
// way "kubectl exec -it" does (WebSocket, falling back to SPDY).
func (c *Client) Exec(ctx context.Context, ns, pod, container string, cmd []string,
	stdin io.Reader, stdout io.Writer, sizes remotecommand.TerminalSizeQueue) error {
	req := c.Kube.CoreV1().RESTClient().Post().
		Resource("pods").Namespace(ns).Name(pod).SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: container,
			Command:   cmd,
			Stdin:     true,
			Stdout:    true,
			TTY:       true,
		}, scheme.ParameterCodec)

	spdy, err := remotecommand.NewSPDYExecutor(c.Config, "POST", req.URL())
	if err != nil {
		return err
	}
	ws, err := remotecommand.NewWebSocketExecutor(c.Config, "GET", req.URL().String())
	if err != nil {
		return err
	}
	exec, err := remotecommand.NewFallbackExecutor(ws, spdy, func(err error) bool {
		return httpstream.IsUpgradeFailure(err) || httpstream.IsHTTPSProxyError(err)
	})
	if err != nil {
		return err
	}
	return exec.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdin:             stdin,
		Stdout:            stdout,
		Tty:               true,
		TerminalSizeQueue: sizes,
	})
}
