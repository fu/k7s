// Package resources suggests label selectors for the filter: it looks at the
// app labels used in a namespace and counts what each selector matches.
package resources

import (
	"context"
	"sort"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/fu/k7s/internal/kube"
)

// kinds are the namespaced views the filter applies to, in display order.
var kinds = []struct {
	name string
	gvr  schema.GroupVersionResource
}{
	{"pods", corev1.SchemeGroupVersion.WithResource("pods")},
	{"secrets", corev1.SchemeGroupVersion.WithResource("secrets")},
}

// Kinds returns the kind names in display order.
func Kinds() []string {
	out := make([]string, len(kinds))
	for i, k := range kinds {
		out[i] = k.name
	}
	return out
}

// selectorKeys are the labels apps are commonly grouped by, most specific first.
var selectorKeys = []string{
	"app.kubernetes.io/instance", "app.kubernetes.io/name", "app", "k8s-app",
	"release", "app.kubernetes.io/part-of",
}

// Candidate is a suggested selector with how many objects of each kind it matches.
type Candidate struct {
	Selector string
	Counts   map[string]int
	Total    int
}

// Selectors suggests selectors from the labels used in ns ("" = all
// namespaces). It reads only names and labels (metadata), never secret values.
func Selectors(ctx context.Context, c *kube.Client, ns string) ([]Candidate, error) {
	bySel := map[string]*Candidate{}
	for _, k := range kinds {
		list, err := c.Meta.Resource(k.gvr).Namespace(ns).List(ctx, metav1.ListOptions{ResourceVersion: "0"})
		if err != nil {
			return nil, err
		}
		for _, o := range list.Items {
			for _, key := range selectorKeys {
				v, ok := o.Labels[key]
				if !ok || v == "" {
					continue
				}
				sel := key + "=" + v
				cand := bySel[sel]
				if cand == nil {
					cand = &Candidate{Selector: sel, Counts: map[string]int{}}
					bySel[sel] = cand
				}
				cand.Counts[k.name]++
				cand.Total++
			}
		}
	}
	out := make([]Candidate, 0, len(bySel))
	for _, cand := range bySel {
		out = append(out, *cand)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Total != out[j].Total {
			return out[i].Total > out[j].Total
		}
		return out[i].Selector < out[j].Selector
	})
	return out, nil
}
