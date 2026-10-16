package tui

import (
	"errors"
	"fmt"
	"io"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/muesli/cancelreader"
	"golang.org/x/term"
	utilexec "k8s.io/client-go/util/exec"

	"github.com/fu/k7s/internal/resources"
)

// Exec steps: Bubble Tea hands the terminal to an interactive program (e.g.
// a shell in a container) while it runs and takes it back afterwards.

type execDoneMsg struct{ err error }

func runExec(e *resources.Exec) tea.Cmd {
	return tea.Exec(&execCmd{e: e}, func(err error) tea.Msg { return execDoneMsg{err} })
}

type execCmd struct {
	e      *resources.Exec
	stdin  io.Reader
	stdout io.Writer
}

func (c *execCmd) SetStdin(r io.Reader)  { c.stdin = r }
func (c *execCmd) SetStdout(w io.Writer) { c.stdout = w }
func (c *execCmd) SetStderr(io.Writer)   {}

func (c *execCmd) Run() error {
	fmt.Fprintf(c.stdout, "\n%s %s\n\n", badge.Render("k7s"), c.e.Banner)

	if f, ok := c.stdin.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		if old, err := term.MakeRaw(int(f.Fd())); err == nil {
			defer term.Restore(int(f.Fd()), old)
		}
	}
	sizes := newSizeQueue(int(os.Stdout.Fd()))
	defer sizes.stop()

	// client-go keeps copying stdin in a goroutine after the remote program
	// exits; without cancelling it, that goroutine swallows the next key
	// pressed in k7s.
	stdin := c.stdin
	if cr, err := cancelreader.NewReader(c.stdin); err == nil {
		defer cr.Close()
		defer cr.Cancel()
		stdin = cr
	}

	err := c.e.Run(stdin, c.stdout, sizes)
	// A non-zero exit of the last command in a shell is not a k7s error.
	var exitErr utilexec.ExitError
	if errors.As(err, &exitErr) {
		return nil
	}
	return err
}
