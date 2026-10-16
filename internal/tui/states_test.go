package tui

import (
	"strings"
	"testing"

	"github.com/fu/k7s/internal/config"
	"github.com/fu/k7s/internal/resources"
)

// podKind is a stateful kind with fixed rows.
type podKind struct {
	resources.Resource
	rows []resources.Row
}

func (k podKind) Name() string             { return "pods" }
func (k podKind) Rows() []resources.Row    { return k.rows }
func (k podKind) States() []string         { return []string{"running", "completed"} }
func (k podKind) Status() []resources.Cell { return []resources.Cell{{Text: "pods"}} }
func (k podKind) Ready() bool              { return true }

func TestHiddenStatesLeaveTheTableButAreCounted(t *testing.T) {
	pods := podKind{rows: []resources.Row{
		{Key: "a", State: "running"}, {Key: "b", State: "completed"}, {Key: "c", State: "completed"},
	}}
	cfg := config.Default() // hides completed pods
	m := &model{kinds: []resources.Resource{pods}, info: Info{Refresh: 5e9}}
	m.page.cfg = cfg

	if got := keys(m.rows(pods)); len(got) != 1 || got[0] != "a" {
		t.Errorf("shown rows: %v", got)
	}
	if !strings.Contains(m.statusLine(), "(+2 hidden)") {
		t.Errorf("status line should say what is hidden: %q", m.statusLine())
	}

	cfg.SetHidden("pods", true)
	if strings.Contains(m.statusLine(), "hidden)") {
		t.Errorf("a section toggled off counts no hidden rows: %q", m.statusLine())
	}
	cfg.SetHidden("pods", false)

	rows := m.stateRows([]resources.Column{{Title: " "}, {Title: "FILTER"}, {Title: "PODS"}})
	if len(rows) != 2 || rows[1].Cells[0].Text != "○" || rows[1].Cells[2].Text != "2" {
		t.Fatalf("state rows: %+v", rows)
	}
	cfg.SetStateHidden("pods", "completed", false)
	if len(m.rows(pods)) != 3 {
		t.Error("showing completed pods again brings them back")
	}
}
