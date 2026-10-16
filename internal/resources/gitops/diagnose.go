package gitops

import (
	"context"
	"fmt"
	"strings"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/fu/k7s/internal/kube"
	"github.com/fu/k7s/internal/resources/pods"
)

// Why an app is not green. Argo CD 3 no longer keeps each resource's health
// in the Application, so "Degraded" often comes without a reason; k7s then
// looks at what the app manages itself: its child apps (app of apps) and
// its workloads — and for an unhealthy workload, its failing pods.

const maxChecks = 40 // managed objects looked at per app

// diagnose returns one line per problem found. apps are all apps, to
// explain child apps without asking the API.
func diagnose(ctx context.Context, c *kube.Client, a App, apps map[string]App) []string {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var out []string
	checks := 0
	for _, m := range a.Manages {
		if checks >= maxChecks {
			break
		}
		switch m.Kind {
		case "Application", "Kustomization", "HelmRelease":
			if child, ok := apps[App{Kind: m.Kind, Namespace: m.Namespace, Name: m.Name}.Key()]; ok && child.Overall() != Green {
				line := fmt.Sprintf("%s %s %s", m.Kind, m.Name, strings.TrimSpace(child.Sync+" "+child.Health))
				if why := child.Explain(); why != "" {
					line += ": " + why
				}
				out = append(out, line)
			}
		case "Deployment", "StatefulSet", "DaemonSet", "Job", "PersistentVolumeClaim":
			checks++
			out = append(out, checkWorkload(ctx, c, m)...)
		}
	}
	return out
}

// checkWorkload reports a managed object that is not healthy, and why.
func checkWorkload(ctx context.Context, c *kube.Client, m Ref) []string {
	get := metav1.GetOptions{}
	switch m.Kind {
	case "Deployment":
		d, err := c.Kube.AppsV1().Deployments(m.Namespace).Get(ctx, m.Name, get)
		if err != nil {
			return missing(m, err)
		}
		want := int32(1)
		if d.Spec.Replicas != nil {
			want = *d.Spec.Replicas
		}
		if d.Status.AvailableReplicas >= want && d.Status.UpdatedReplicas >= want {
			return nil
		}
		line := fmt.Sprintf("Deployment %s: %d/%d available", m.Name, d.Status.AvailableReplicas, want)
		for _, cond := range d.Status.Conditions {
			if cond.Status == corev1.ConditionFalse && cond.Message != "" {
				line += ", " + cond.Message
			}
		}
		return append([]string{line}, failingPods(ctx, c, m.Namespace, d.Spec.Selector)...)
	case "StatefulSet":
		s, err := c.Kube.AppsV1().StatefulSets(m.Namespace).Get(ctx, m.Name, get)
		if err != nil {
			return missing(m, err)
		}
		want := int32(1)
		if s.Spec.Replicas != nil {
			want = *s.Spec.Replicas
		}
		if s.Status.ReadyReplicas >= want {
			return nil
		}
		line := fmt.Sprintf("StatefulSet %s: %d/%d ready", m.Name, s.Status.ReadyReplicas, want)
		return append([]string{line}, failingPods(ctx, c, m.Namespace, s.Spec.Selector)...)
	case "DaemonSet":
		s, err := c.Kube.AppsV1().DaemonSets(m.Namespace).Get(ctx, m.Name, get)
		if err != nil {
			return missing(m, err)
		}
		if s.Status.NumberUnavailable == 0 {
			return nil
		}
		line := fmt.Sprintf("DaemonSet %s: %d of %d unavailable", m.Name, s.Status.NumberUnavailable, s.Status.DesiredNumberScheduled)
		return append([]string{line}, failingPods(ctx, c, m.Namespace, s.Spec.Selector)...)
	case "Job":
		j, err := c.Kube.BatchV1().Jobs(m.Namespace).Get(ctx, m.Name, get)
		if err != nil {
			return missing(m, err)
		}
		for _, cond := range j.Status.Conditions {
			if cond.Type == batchv1.JobFailed && cond.Status == corev1.ConditionTrue {
				return append([]string{"Job " + m.Name + " failed: " + cond.Message}, failingPods(ctx, c, m.Namespace, j.Spec.Selector)...)
			}
		}
	case "PersistentVolumeClaim":
		p, err := c.Kube.CoreV1().PersistentVolumeClaims(m.Namespace).Get(ctx, m.Name, get)
		if err != nil {
			return missing(m, err)
		}
		if p.Status.Phase != corev1.ClaimBound {
			return []string{fmt.Sprintf("PersistentVolumeClaim %s: %s", m.Name, p.Status.Phase)}
		}
	}
	return nil
}

// missing reports a managed object that is gone (a reason to be degraded).
func missing(m Ref, err error) []string {
	if strings.Contains(err.Error(), "not found") {
		return []string{fmt.Sprintf("%s %s: missing", m.Kind, m.Name)}
	}
	return nil // e.g. forbidden: say nothing rather than guess
}

// failingPods lists up to three pods of a workload that are not running
// well, each with why (a waiting reason or the last exit).
func failingPods(ctx context.Context, c *kube.Client, ns string, sel *metav1.LabelSelector) []string {
	if sel == nil {
		return nil
	}
	selector, err := metav1.LabelSelectorAsSelector(sel)
	if err != nil {
		return nil
	}
	list, err := c.Kube.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{LabelSelector: selector.String()})
	if err != nil {
		return nil
	}
	var out []string
	for i := range list.Items {
		p := pods.FromPod(&list.Items[i])
		if h := p.Health(); h == pods.Healthy || h == pods.Finished {
			continue
		}
		line := fmt.Sprintf("  pod %s %s %s", p.Name, p.Status, p.ReadyString())
		switch {
		case p.WaitMessage() != "":
			line += ": " + p.WaitMessage()
		case !p.LastExit.IsZero():
			line += ": last exit " + p.LastExit.Describe()
		}
		if p.Restarts > 0 {
			line += fmt.Sprintf(" (%d restarts)", p.Restarts)
		}
		out = append(out, line)
		if len(out) == 3 {
			break
		}
	}
	return out
}
