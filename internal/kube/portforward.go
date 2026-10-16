package kube

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"

	"k8s.io/apimachinery/pkg/util/httpstream"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
)

// PortForward forwards localhost:local to port remote of a pod, the same way
// "kubectl port-forward" does (WebSocket, falling back to SPDY). ready is
// closed once it listens. It blocks until ctx is done or the forward fails.
func (c *Client) PortForward(ctx context.Context, ns, pod string, local, remote int, ready chan struct{}) error {
	req := c.Kube.CoreV1().RESTClient().Post().Resource("pods").Namespace(ns).Name(pod).SubResource("portforward")

	transport, upgrader, err := spdy.RoundTripperFor(c.Config)
	if err != nil {
		return err
	}
	var dialer httpstream.Dialer = spdy.NewDialer(upgrader, &http.Client{Transport: transport}, "POST", req.URL())
	if ws, err := portforward.NewSPDYOverWebsocketDialer(req.URL(), c.Config); err == nil {
		dialer = portforward.NewFallbackDialer(ws, dialer, func(err error) bool {
			return httpstream.IsUpgradeFailure(err) || httpstream.IsHTTPSProxyError(err)
		})
	}

	stop := make(chan struct{})
	go func() {
		<-ctx.Done()
		close(stop)
	}()
	fw, err := portforward.NewOnAddresses(dialer, []string{"localhost"},
		[]string{fmt.Sprintf("%d:%d", local, remote)}, stop, ready, io.Discard, io.Discard)
	if err != nil {
		return err
	}
	return fw.ForwardPorts()
}

// FreeLocalPort returns want if it can be listened on (and is not
// privileged), otherwise any free port.
func FreeLocalPort(want int) (int, error) {
	if want >= 1024 {
		if l, err := net.Listen("tcp", fmt.Sprintf("localhost:%d", want)); err == nil {
			l.Close()
			return want, nil
		}
	}
	l, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}
