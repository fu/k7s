package commands

import (
	"context"
	"fmt"
	"io"

	"k8s.io/client-go/tools/remotecommand"

	"github.com/fu/k7s/internal/kube"
	"github.com/fu/k7s/internal/resources"
	"github.com/fu/k7s/internal/resources/pods"
)

// Shell runs an interactive shell (bash, else sh) in a running container,
// like "kubectl exec -it". Type exit to come back to k7s.
var Shell = resources.Command{
	Name: "shell",
	Run: func(t resources.Target) resources.Step {
		return pickContainer(t, "shell", true, func(pod pods.Row, c pods.Container) resources.Step {
			if c.State != "Running" {
				t.Host.Notice(fmt.Sprintf("no shell: container %s is %s", c.Name, c.State))
				return nil
			}
			client := t.Host.Client()
			return &resources.Exec{
				Banner: fmt.Sprintf("shell in %s/%s [%s] — type 'exit' to return to k7s", pod.Namespace, pod.Name, c.Name),
				Run: func(stdin io.Reader, stdout io.Writer, sizes remotecommand.TerminalSizeQueue) error {
					return client.Exec(context.Background(), pod.Namespace, pod.Name, c.Name, kube.ShellCommand, stdin, stdout, sizes)
				},
			}
		})
	},
}
