package commands

import (
	"testing"

	"github.com/fu/k7s/internal/resources"
	"github.com/fu/k7s/internal/resources/pods"
	"github.com/fu/k7s/internal/resources/restest"
)

func TestSeveralContainersAskWhich(t *testing.T) {
	h := &restest.Host{}
	opened := ""
	open := func(_ pods.Row, c pods.Container) resources.Step { opened = c.Name; return &resources.Viewer{} }
	pod := pods.Row{Namespace: "ns", Name: "a", Containers: []pods.Container{
		{Name: "init", Kind: pods.KindInit}, {Name: "app"}, {Name: "proxy", Kind: pods.KindSidecar},
	}}

	p, ok := choose(h, pod, nil, "logs", false, open).(*resources.Picker)
	if !ok || len(p.Rows) != 3 || p.Current != "app" {
		t.Fatalf("logs: want a picker over 3 containers starting on app, got %+v", p)
	}
	if _, ok := p.Pick("proxy").(*resources.Viewer); !ok || opened != "proxy" {
		t.Errorf("picking proxy opens it, opened %q", opened)
	}
	if p, ok := choose(h, pod, nil, "shell", true, open).(*resources.Picker); !ok || len(p.Rows) != 2 {
		t.Fatalf("shell: init containers are not offered, got %+v", p)
	}
	single := pods.Row{Namespace: "ns", Name: "b", Containers: []pods.Container{{Name: "app"}}}
	if _, ok := choose(h, single, nil, "logs", false, open).(*resources.Viewer); !ok {
		t.Error("a single container opens straight away")
	}
}

func TestCommandNamesAreUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range All() {
		if seen[c.Name] || c.Run == nil {
			t.Errorf("command %q: duplicate or without Run", c.Name)
		}
		seen[c.Name] = true
	}
}
