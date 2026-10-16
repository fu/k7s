// Package restest helps testing kinds: a Host that records the change log.
package restest

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/fu/k7s/internal/kube"
	"github.com/fu/k7s/internal/resources"
)

// Host records what a kind logs. Its namespace is NS ("ns" if empty).
type Host struct {
	Changes []resources.Change
	Refs    []resources.Ref // what the other sections list
	NS      string
	Filter  string
}

func (h *Host) Context() context.Context { return context.Background() }
func (h *Host) Client() *kube.Client     { return nil }
func (h *Host) Selector() string         { return h.Filter }
func (h *Host) Refresh() time.Duration   { return 5 * time.Second }
func (h *Host) Send(tea.Msg)             {}
func (h *Host) Log(c resources.Change)   { h.Changes = append(h.Changes, c) }
func (h *Host) Notice(string)            {}
func (h *Host) Listed() []resources.Ref  { return h.Refs }
func (h *Host) Pulse(time.Time)          {}

func (h *Host) Namespace() string {
	if h.NS == "" {
		return "ns"
	}
	return h.NS
}
