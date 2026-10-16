package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/muesli/cancelreader"
	"golang.org/x/term"
	utilexec "k8s.io/client-go/util/exec"

	"k7s/internal/kube"
)

type shellDoneMsg struct{ err error }

// shellCmd runs an interactive shell in a container. Bubble Tea hands over
// the terminal while it runs (tea.Exec) and takes it back afterwards.
type shellCmd struct {
	client             *kube.Client
	ns, pod, container string
	stdin              io.Reader
	stdout             io.Writer
}

func (s *shellCmd) SetStdin(r io.Reader)  { s.stdin = r }
func (s *shellCmd) SetStdout(w io.Writer) { s.stdout = w }
func (s *shellCmd) SetStderr(io.Writer)   {}

func (s *shellCmd) Run() error {
	fmt.Fprintf(s.stdout, "\n%s shell in %s/%s [%s] — type 'exit' to return to k7s\n\n",
		stBadge.Render("k7s"), s.ns, s.pod, s.container)

	if f, ok := s.stdin.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		if old, err := term.MakeRaw(int(f.Fd())); err == nil {
			defer term.Restore(int(f.Fd()), old)
		}
	}
	sizes := newSizeQueue(int(os.Stdout.Fd()))
	defer sizes.stop()

	// client-go keeps copying stdin in a goroutine after the remote shell
	// exits; without cancelling it, that goroutine swallows the next key
	// pressed in k7s.
	var stdin io.Reader = s.stdin
	if cr, err := cancelreader.NewReader(s.stdin); err == nil {
		defer cr.Close()
		defer cr.Cancel()
		stdin = cr
	}

	err := s.client.Exec(context.Background(), s.ns, s.pod, s.container, kube.ShellCommand, stdin, s.stdout, sizes)
	// A non-zero exit of the last command in the shell is not a k7s error.
	var exitErr utilexec.ExitError
	if errors.As(err, &exitErr) {
		return nil
	}
	return err
}
