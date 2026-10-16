package tui

import (
	"context"
	"fmt"
	"sort"

	tea "github.com/charmbracelet/bubbletea"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/fu/k7s/internal/kube"
	"github.com/fu/k7s/internal/resources"
	"github.com/fu/k7s/internal/resources/pods"
)

// openNamespaces lists the namespaces with their pod health and usage,
// refreshed while open.
func (m *model) openNamespaces() tea.Cmd {
	client, ns := m.client, m.info.Namespace
	count := func(n int, ok bool, tone resources.Tone) resources.Cell {
		switch {
		case !ok:
			return resources.Cell{Text: "-", Tone: resources.Dim}
		case n == 0:
			return resources.Cell{Text: "0", Tone: resources.Dim}
		}
		return resources.Cell{Text: fmt.Sprint(n), Tone: tone}
	}
	p := &resources.Picker{
		Title: "namespaces",
		Columns: []resources.Column{
			{Title: " "}, {Title: "NAMESPACE", Shrink: true}, {Title: "STATUS"},
			{Title: "PODS", Right: true}, {Title: "RUNNING", Right: true}, {Title: "PENDING", Right: true}, {Title: "FAILING", Right: true},
			{Title: "CPU", Right: true}, {Title: "MEM", Right: true}, {Title: "AGE"},
		},
		Current: ns,
		Hint:    "switch",
		Every:   m.info.Refresh,
		Load: func(ctx context.Context) ([]resources.Row, error) {
			list, err := namespaceSummaries(ctx, client)
			if err != nil {
				// Listing namespaces may be forbidden; still offer the
				// context's own namespace so it can be used.
				list = []nsSummary{{Name: client.Namespace}}
			}
			rows := make([]resources.Row, len(list))
			for i, s := range list {
				status := resources.Cell{Text: s.Status, Tone: resources.Dim}
				if s.Status == "Terminating" {
					status.Tone = resources.Bad
				}
				rows[i] = resources.Row{Key: s.Name, Cells: []resources.Cell{
					current(s.Name == ns), {Text: s.Name}, status,
					count(s.Pods, s.HasPods, resources.Strong),
					count(s.Healthy, s.HasPods, resources.Good),
					count(s.Progressing, s.HasPods, resources.Busy),
					count(s.Failing, s.HasPods, resources.Alarm),
					{Text: dash(s.HasMetrics, kube.CPU(s.CPU))},
					{Text: dash(s.HasMetrics, kube.Mem(s.Mem))},
					{Text: kube.Age(s.Created.Time), Tone: resources.Dim},
				}}
			}
			return rows, err
		},
	}
	p.Pick = func(name string) resources.Step {
		if name == m.info.Namespace {
			return nil
		}
		m.info.Namespace = name
		return resources.Cmd(m.scopeChanged())
	}
	return m.openScope("namespaces", p)
}

func dash(ok bool, s string) string {
	if !ok {
		return "-"
	}
	return s
}

// nsSummary is one namespace with pod health counts and summed usage.
type nsSummary struct {
	Name, Status                        string
	Created                             metav1.Time
	HasPods                             bool // needs a cluster-wide pod list
	Pods, Healthy, Progressing, Failing int
	HasMetrics                          bool
	CPU, Mem                            int64
}

// namespaceSummaries lists all namespaces. Counts and metrics are best
// effort: missing RBAC or metrics-server just leaves them unknown.
func namespaceSummaries(ctx context.Context, c *kube.Client) ([]nsSummary, error) {
	nsList, err := c.Kube.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	out := make([]nsSummary, len(nsList.Items))
	byName := make(map[string]*nsSummary, len(out))
	for i, ns := range nsList.Items {
		out[i] = nsSummary{Name: ns.Name, Status: string(ns.Status.Phase), Created: ns.CreationTimestamp}
		byName[ns.Name] = &out[i]
	}

	// ResourceVersion "0" is served from the API server's watch cache.
	if podList, err := c.Kube.CoreV1().Pods(metav1.NamespaceAll).List(ctx, metav1.ListOptions{ResourceVersion: "0"}); err == nil {
		for i := range out {
			out[i].HasPods = true
		}
		for i := range podList.Items {
			s := byName[podList.Items[i].Namespace]
			if s == nil {
				continue
			}
			s.Pods++
			switch pods.FromPod(&podList.Items[i]).Health() {
			case pods.Healthy:
				s.Healthy++
			case pods.Progressing:
				s.Progressing++
			case pods.Failing:
				s.Failing++
			}
		}
	}

	if mList, err := c.Metrics.MetricsV1beta1().PodMetricses(metav1.NamespaceAll).List(ctx, metav1.ListOptions{}); err == nil {
		for i := range out {
			out[i].HasMetrics = true
		}
		for _, pm := range mList.Items {
			if s := byName[pm.Namespace]; s != nil {
				for _, ct := range pm.Containers {
					s.CPU += ct.Usage.Cpu().MilliValue()
					s.Mem += ct.Usage.Memory().Value()
				}
			}
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
