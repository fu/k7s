package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/fu/k7s/internal/kube"
	"github.com/fu/k7s/internal/resources"
)

// The filter is a label selector for every section. Its picker suggests the
// selectors found on the labeled kinds of the namespace, plus "everything".

// selectorKeys are the labels apps are commonly grouped by, most specific first.
var selectorKeys = []string{
	"app.kubernetes.io/instance", "app.kubernetes.io/name", "app", "k8s-app",
	"release", "app.kubernetes.io/part-of",
}

const everything = "" // the row key of "no filter"

func (m *model) openFilter() tea.Cmd {
	var kinds []resources.Resource
	cols := []resources.Column{{Title: " "}, {Title: "FILTER", Shrink: true}}
	for _, k := range m.kinds {
		if _, ok := k.(resources.Labeled); ok {
			kinds = append(kinds, k)
			cols = append(cols, resources.Column{Title: strings.ToUpper(k.Name()), Right: true})
		}
	}
	client, ns, sel := m.client, m.info.Namespace, m.info.Selector
	p := &resources.Picker{
		Title:   "filter │ ns " + ns,
		Columns: cols,
		Current: sel,
		Hint:    "apply",
		Load: func(ctx context.Context) ([]resources.Row, error) {
			counts, order, err := selectors(ctx, client, ns, kinds)
			all := []resources.Cell{current(sel == everything), {Text: "everything (no filter)", Tone: resources.Info}}
			rows := []resources.Row{{Key: everything, Cells: all}}
			for _, s := range order {
				cs := []resources.Cell{current(s == sel), {Text: s}}
				for _, k := range kinds {
					n := counts[s][k.Name()]
					tone := resources.Strong
					if n == 0 {
						tone = resources.Dim
					}
					cs = append(cs, resources.Cell{Text: fmt.Sprint(n), Tone: tone})
				}
				rows = append(rows, resources.Row{Key: s, Cells: cs})
			}
			return rows, err
		},
		Pick: func(s string) resources.Step {
			if s == m.info.Selector {
				return nil
			}
			m.info.Selector = s
			return resources.Cmd(m.scopeChanged())
		},
	}
	return m.openScope("filter", p)
}

// selectors suggests selectors from the labels the kinds use in ns, counting
// per kind what each matches, most matches first. It reads only names and
// labels (metadata), never secret values.
func selectors(ctx context.Context, c *kube.Client, ns string, kinds []resources.Resource) (map[string]map[string]int, []string, error) {
	counts := map[string]map[string]int{}
	total := map[string]int{}
	for _, k := range kinds {
		list, err := c.Meta.Resource(k.(resources.Labeled).GVR()).Namespace(ns).List(ctx, metav1.ListOptions{ResourceVersion: "0"})
		if err != nil {
			return nil, nil, err
		}
		for _, o := range list.Items {
			for _, key := range selectorKeys {
				if v := o.Labels[key]; v != "" {
					s := key + "=" + v
					if counts[s] == nil {
						counts[s] = map[string]int{}
					}
					counts[s][k.Name()]++
					total[s]++
				}
			}
		}
	}
	order := make([]string, 0, len(counts))
	for s := range counts {
		order = append(order, s)
	}
	sort.Slice(order, func(i, j int) bool {
		if total[order[i]] != total[order[j]] {
			return total[order[i]] > total[order[j]]
		}
		return order[i] < order[j]
	})
	return counts, order, nil
}
