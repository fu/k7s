package gitops

import (
	"fmt"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var argoApps = schema.GroupVersionResource{Group: "argoproj.io", Version: "v1alpha1", Resource: "applications"}

// fromArgo maps an argoproj.io/v1alpha1 Application to an App.
func fromArgo(u *unstructured.Unstructured) App {
	o := u.Object
	str := func(fields ...string) string {
		v, _, _ := unstructured.NestedString(o, fields...)
		return v
	}
	a := App{
		Kind:      "Application",
		Namespace: u.GetNamespace(),
		Name:      u.GetName(),
		Sync:      orUnknown(str("status", "sync", "status")),
		Health:    orUnknown(str("status", "health", "status")),
		OpPhase:   str("status", "operationState", "phase"),
		OpMessage: firstLine(str("status", "operationState", "message")),
		DestNS:    str("spec", "destination", "namespace"),
		Revision:  str("status", "sync", "revision"),
	}
	if t, err := time.Parse(time.RFC3339, str("status", "operationState", "finishedAt")); err == nil {
		a.SyncedAt = t
	}
	// automated: {} means on; newer Argo CD also has automated.enabled.
	auto := false
	if m, found, _ := unstructured.NestedMap(o, "spec", "syncPolicy", "automated"); found {
		enabled, ok := m["enabled"].(bool)
		auto = !ok || enabled
	}

	// Single source, or the first of several (multi-source apps).
	src, found, _ := unstructured.NestedMap(o, "spec", "source")
	extra := 0
	if !found {
		if list, ok, _ := unstructured.NestedSlice(o, "spec", "sources"); ok && len(list) > 0 {
			src, _ = list[0].(map[string]any)
			extra = len(list) - 1
		}
		if revs, ok, _ := unstructured.NestedStringSlice(o, "status", "sync", "revisions"); ok && len(revs) > 0 {
			a.Revision = revs[0]
		}
	}
	get := func(k string) string { s, _ := src[k].(string); return s }
	a.Source = describeSource(get("repoURL"), get("path"), get("chart"))
	if extra > 0 {
		a.Source += fmt.Sprintf(" +%d", extra)
	}
	a.Revision = shortRevision(a.Revision)

	switch {
	case a.syncFailed():
		a.SyncL = Red
	case a.Sync == "Synced":
		a.SyncL = Green
	case a.Sync == "OutOfSync":
		a.SyncL = Amber
	}
	switch a.Health {
	case "Healthy":
		a.HealthL = Green
	case "Progressing", "Suspended":
		a.HealthL = Amber
	case "Degraded", "Missing":
		a.HealthL = Red
	}

	// Why it is not green: auto-sync, failed sync, then the resources that
	// are not healthy or not in sync.
	if a.Sync == "OutOfSync" && !auto {
		a.why("auto-sync off")
	}
	if a.syncFailed() && a.OpMessage != "" {
		a.why("sync " + strings.ToLower(a.OpPhase) + ": " + a.OpMessage)
	}
	var problems []string
	resources, _, _ := unstructured.NestedSlice(o, "status", "resources")
	for _, r := range resources {
		m, ok := r.(map[string]any)
		if !ok {
			continue
		}
		kind, _ := m["kind"].(string)
		name, _ := m["name"].(string)
		syncStatus, _ := m["status"].(string)
		var hs, hmsg string
		if h, ok := m["health"].(map[string]any); ok {
			hs, _ = h["status"].(string)
			hmsg, _ = h["message"].(string)
		}
		switch {
		case hs != "" && hs != "Healthy":
			p := fmt.Sprintf("%s %s %s", kind, name, hs)
			if hmsg = firstLine(hmsg); hmsg != "" {
				p += ": " + hmsg
			}
			problems = append(problems, p)
		case syncStatus == "OutOfSync":
			problems = append(problems, fmt.Sprintf("%s %s OutOfSync", kind, name))
		}
	}
	a.Problems = problems
	return a
}
