//go:build !windows

package tui

import (
	"os"
	"os/signal"
	"syscall"

	"golang.org/x/term"
	"k8s.io/client-go/tools/remotecommand"
)

// sizeQueue forwards local terminal resizes to the remote TTY.
type sizeQueue struct {
	fd   int
	ch   chan remotecommand.TerminalSize
	sig  chan os.Signal
	done chan struct{}
}

func newSizeQueue(fd int) *sizeQueue {
	q := &sizeQueue{
		fd:   fd,
		ch:   make(chan remotecommand.TerminalSize, 1),
		sig:  make(chan os.Signal, 1),
		done: make(chan struct{}),
	}
	q.push()
	signal.Notify(q.sig, syscall.SIGWINCH)
	go func() {
		for {
			select {
			case <-q.sig:
				q.push()
			case <-q.done:
				return
			}
		}
	}()
	return q
}

func (q *sizeQueue) push() {
	w, h, err := term.GetSize(q.fd)
	if err != nil {
		return
	}
	select { // keep only the latest size
	case <-q.ch:
	default:
	}
	q.ch <- remotecommand.TerminalSize{Width: uint16(w), Height: uint16(h)}
}

func (q *sizeQueue) Next() *remotecommand.TerminalSize {
	select {
	case s := <-q.ch:
		return &s
	case <-q.done:
		return nil
	}
}

func (q *sizeQueue) stop() {
	signal.Stop(q.sig)
	close(q.done)
}
