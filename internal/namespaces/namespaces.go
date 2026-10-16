// Package namespaces summarizes the namespaces of a cluster for the picker.
package namespaces

import (
	"context"
	"sort"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/fu/k7s/internal/kube"
	"github.com/fu/k7s/internal/pods"
)

// Summary is one row of the namespace picker.
type Summary struct {
	Name    string
	Status  string
	Created time.Time

	HasPods                             bool // pod counts are known (needs cluster-wide pod list)
	Pods, Healthy, Progressing, Failing int
	HasMetrics                          bool
	CPU, Mem                            int64
}

// List returns all namespaces with pod health counts and summed usage.
// Counts and metrics are best effort: missing RBAC or metrics-server just
// leaves them unknown.
func List(ctx context.Context, c *kube.Client) ([]Summary, error) {
	nsList, err := c.Kube.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	out := make([]Summary, len(nsList.Items))
	byName := make(map[string]*Summary, len(out))
	for i, ns := range nsList.Items {
		out[i] = Summary{Name: ns.Name, Status: string(ns.Status.Phase), Created: ns.CreationTimestamp.Time}
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
			s := byName[pm.Namespace]
			if s == nil {
				continue
			}
			for _, ct := range pm.Containers {
				s.CPU += ct.Usage.Cpu().MilliValue()
				s.Mem += ct.Usage.Memory().Value()
			}
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
