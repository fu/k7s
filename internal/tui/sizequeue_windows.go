//go:build windows

package tui

import (
	"golang.org/x/term"
	"k8s.io/client-go/tools/remotecommand"
)

// sizeQueue reports the initial terminal size only (no SIGWINCH on Windows).
type sizeQueue struct {
	ch   chan remotecommand.TerminalSize
	done chan struct{}
}

func newSizeQueue(fd int) *sizeQueue {
	q := &sizeQueue{ch: make(chan remotecommand.TerminalSize, 1), done: make(chan struct{})}
	if w, h, err := term.GetSize(fd); err == nil {
		q.ch <- remotecommand.TerminalSize{Width: uint16(w), Height: uint16(h)}
	}
	return q
}

func (q *sizeQueue) Next() *remotecommand.TerminalSize {
	select {
	case s := <-q.ch:
		return &s
	case <-q.done:
		return nil
	}
}

func (q *sizeQueue) stop() { close(q.done) }
