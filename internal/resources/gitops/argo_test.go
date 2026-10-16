package gitops

import (
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestFromArgo(t *testing.T) {
	u := &unstructured.Unstructured{Object: map[string]any{
		"metadata": map[string]any{"namespace": "argocd", "name": "shop"},
		"spec": map[string]any{
			"destination": map[string]any{"namespace": "shop"},
			"sources": []any{
				map[string]any{"repoURL": "https://github.com/example/platform-config.git", "path": "apps/shop"},
				map[string]any{"repoURL": "https://github.com/example/shop-deploy.git"},
			},
			"syncPolicy": map[string]any{"automated": map[string]any{"enabled": false, "prune": true}},
		},
		"status": map[string]any{
			"sync":   map[string]any{"status": "OutOfSync", "revisions": []any{"f91dfdcc32b3a25e07cdede1d9569faf976555c2", "abc"}},
			"health": map[string]any{"status": "Degraded"},
			"resources": []any{
				map[string]any{"kind": "Deployment", "name": "planner", "status": "Synced",
					"health": map[string]any{"status": "Degraded", "message": "Deployment exceeded its progress deadline"}},
				map[string]any{"kind": "ConfigMap", "name": "cfg", "status": "OutOfSync"},
				map[string]any{"kind": "Service", "name": "ok", "status": "Synced", "health": map[string]any{"status": "Healthy"}},
			},
		},
	}}

	a := fromArgo(u)
	if a.Source != "platform-config/apps/shop +1" || a.Revision != "f91dfdc" {
		t.Errorf("unexpected source/revision: %q %q", a.Source, a.Revision)
	}
	if a.SyncLight() != Amber || a.HealthLight() != Red || a.Overall() != Red {
		t.Errorf("unexpected lights: %v %v", a.SyncLight(), a.HealthLight())
	}
	if got := a.Explain(); got != "auto-sync off" {
		t.Errorf("Explain() = %q", got)
	}
	want := []string{"Deployment planner Degraded: Deployment exceeded its progress deadline", "ConfigMap cfg OutOfSync"}
	if strings.Join(a.Problems, "|") != strings.Join(want, "|") {
		t.Errorf("Problems = %q, want %q (healthy, synced resources left out)", a.Problems, want)
	}
}

func TestHelmSource(t *testing.T) {
	if got := describeSource("https://charts.jetstack.io", "", "cert-manager"); got != "cert-manager (helm)" {
		t.Errorf("got %q", got)
	}
	if got := shortRevision("v1.18.2"); got != "v1.18.2" {
		t.Errorf("chart versions must not be shortened: %q", got)
	}
}
