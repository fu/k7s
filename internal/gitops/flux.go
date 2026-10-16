package gitops

import (
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// condition is one entry of a Flux object's status.conditions.
type condition struct {
	status, reason, message string
	at                      time.Time
}

func conditions(o map[string]any) map[string]condition {
	out := map[string]condition{}
	list, _, _ := unstructured.NestedSlice(o, "status", "conditions")
	for _, c := range list {
		m, ok := c.(map[string]any)
		if !ok {
			continue
		}
		s := func(k string) string { v, _ := m[k].(string); return v }
		at, _ := time.Parse(time.RFC3339, s("lastTransitionTime"))
		out[s("type")] = condition{status: s("status"), reason: s("reason"), message: firstLine(s("message")), at: at}
	}
	return out
}

// fluxCommon fills what Kustomizations and HelmReleases share: the Ready
// condition (SYNC light) and suspension.
func fluxCommon(u *unstructured.Unstructured, kind string) (App, map[string]condition) {
	o := u.Object
	a := App{Kind: kind, Namespace: u.GetNamespace(), Name: u.GetName()}
	a.DestNS, _, _ = unstructured.NestedString(o, "spec", "targetNamespace")
	if a.DestNS == "" {
		a.DestNS = a.Namespace
	}
	conds := conditions(o)
	ready, hasReady := conds["Ready"]
	a.SyncedAt = ready.at

	suspended, _, _ := unstructured.NestedBool(o, "spec", "suspend")
	switch {
	case suspended:
		a.Sync, a.SyncL = "Suspended", Amber
		a.why("suspended")
	case hasReady && ready.status == "True":
		a.Sync, a.SyncL = "Ready", Green
	case hasReady && ready.status == "False":
		a.Sync, a.SyncL = "Failed", Red
		a.why(reasonMessage(ready))
	default:
		a.Sync, a.SyncL = "Reconciling", Amber
		if ready.message != "" {
			a.why(ready.message)
		}
	}
	return a, conds
}

func reasonMessage(c condition) string {
	switch {
	case c.reason != "" && c.message != "":
		return c.reason + ": " + c.message
	case c.message != "":
		return c.message
	}
	return c.reason
}

// fromKustomization maps a kustomize.toolkit.fluxcd.io Kustomization.
func fromKustomization(u *unstructured.Unstructured) App {
	a, conds := fluxCommon(u, "Kustomization")
	o := u.Object
	srcName, _, _ := unstructured.NestedString(o, "spec", "sourceRef", "name")
	dir, _, _ := unstructured.NestedString(o, "spec", "path")
	a.Source = describeSource(srcName, dir, "")
	rev, _, _ := unstructured.NestedString(o, "status", "lastAppliedRevision")
	a.Revision = shortRevision(rev)

	// HEALTH: the Healthy condition (set when health checks / wait are on),
	// otherwise "applied fine" counts as healthy.
	healthy, ok := conds["Healthy"]
	switch {
	case ok && healthy.status == "True":
		a.Health, a.HealthL = "Healthy", Green
	case ok && healthy.status == "False":
		a.Health, a.HealthL = "Unhealthy", Red
		a.why(reasonMessage(healthy))
	case ok:
		a.Health, a.HealthL = "Progressing", Amber
	case a.SyncL == Green:
		a.Health, a.HealthL = "Healthy", Green
	default:
		a.Health = "Unknown"
	}
	return a
}

// fromHelmRelease maps a helm.toolkit.fluxcd.io HelmRelease.
func fromHelmRelease(u *unstructured.Unstructured) App {
	a, conds := fluxCommon(u, "HelmRelease")
	o := u.Object
	chart, _, _ := unstructured.NestedString(o, "spec", "chart", "spec", "chart")
	if chart == "" { // v2 chartRef to an OCIRepository / HelmChart
		chart, _, _ = unstructured.NestedString(o, "spec", "chartRef", "name")
	}
	a.Source = describeSource("", "", chart)
	rev, _, _ := unstructured.NestedString(o, "status", "lastAttemptedRevision")
	if history, ok, _ := unstructured.NestedSlice(o, "status", "history"); ok && len(history) > 0 {
		if h, ok := history[0].(map[string]any); ok {
			if v, ok := h["chartVersion"].(string); ok && v != "" {
				rev = v
			}
		}
	}
	a.Revision = shortRevision(rev)

	// HEALTH: did the release (and its tests, if any) succeed?
	released, hasReleased := conds["Released"]
	tests, hasTests := conds["TestSuccess"]
	switch {
	case hasReleased && released.status == "False":
		a.Health, a.HealthL = "ReleaseFailed", Red
		a.why(reasonMessage(released))
	case hasTests && tests.status == "False":
		a.Health, a.HealthL = "TestFailed", Red
		a.why(reasonMessage(tests))
	case a.SyncL == Green:
		a.Health, a.HealthL = "Healthy", Green
	case a.SyncL == Red:
		a.Health, a.HealthL = "Unhealthy", Red
	default:
		a.Health, a.HealthL = "Progressing", Amber
	}
	return a
}
