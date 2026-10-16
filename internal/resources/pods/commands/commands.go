// Package commands are what a pod row can do: logs, previous-logs, shell,
// forward. Which of them the page offers, and on which keys, is up to the
// config.
//
//	containers.go  pick a container (logs and shell need one)
//	logs.go        logs, previous-logs
//	shell.go       shell
//	forward.go     forward
package commands

import (
	"github.com/fu/k7s/internal/resources"
	"github.com/fu/k7s/internal/resources/pods"
)

// All are the pods' commands, in hint order.
func All() []resources.Command {
	return []resources.Command{Logs, PreviousLogs, Shell, Forward}
}

// pod is the target's pod (live), if it still exists.
func pod(t resources.Target) (*pods.Resource, pods.Row, bool) {
	r, ok := t.Kind.(*pods.Resource)
	if !ok {
		return nil, pods.Row{}, false
	}
	p, ok := r.Pod(t.Key)
	return r, p, ok
}
