package gitops

import (
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func fluxObj(spec map[string]any, conds ...map[string]any) *unstructured.Unstructured {
	list := make([]any, len(conds))
	for i, c := range conds {
		list[i] = c
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"metadata": map[string]any{"namespace": "flux-system", "name": "apps"},
		"spec":     spec,
		"status": map[string]any{
			"conditions":          list,
			"lastAppliedRevision": "main@sha1:f91dfdcc32b3a25e07cdede1d9569faf976555c2",
		},
	}}
}

func cond(typ, status, reason, msg string) map[string]any {
	return map[string]any{"type": typ, "status": status, "reason": reason, "message": msg,
		"lastTransitionTime": "2026-10-16T08:00:00Z"}
}

func TestKustomizationReady(t *testing.T) {
	a := fromKustomization(fluxObj(map[string]any{
		"sourceRef": map[string]any{"kind": "GitRepository", "name": "flux-system"},
		"path":      "./clusters/prod/apps",
	}, cond("Ready", "True", "ReconciliationSucceeded", "Applied revision: main@sha1:f91dfdc")))

	if a.Sync != "Ready" || a.Health != "Healthy" || a.Overall() != Green {
		t.Errorf("want Ready/Healthy green, got %s/%s %v", a.Sync, a.Health, a.Overall())
	}
	if a.Source != "flux-system/clusters/prod/apps" || a.Revision != "main@f91dfdc" || a.DestNS != "flux-system" {
		t.Errorf("unexpected source/revision/dest: %q %q %q", a.Source, a.Revision, a.DestNS)
	}
	if a.SyncedAt.IsZero() || a.Explain() != "" {
		t.Errorf("want sync time and no explanation, got %v %q", a.SyncedAt, a.Explain())
	}
}

func TestKustomizationFailedExplainsWhy(t *testing.T) {
	a := fromKustomization(fluxObj(map[string]any{"targetNamespace": "web"},
		cond("Ready", "False", "BuildFailed", "kustomize build failed: accumulating resources\nmore detail"),
		cond("Healthy", "False", "HealthCheckFailed", "Deployment/web/api not ready")))

	if a.SyncLight() != Red || a.HealthLight() != Red || a.DestNS != "web" {
		t.Errorf("want red/red in web, got %v/%v %q", a.SyncLight(), a.HealthLight(), a.DestNS)
	}
	want := "BuildFailed: kustomize build failed: accumulating resources, HealthCheckFailed: Deployment/web/api not ready"
	if a.Explain() != want {
		t.Errorf("Explain() = %q, want %q", a.Explain(), want)
	}
}

func TestSuspendedIsAmber(t *testing.T) {
	a := fromKustomization(fluxObj(map[string]any{"suspend": true}, cond("Ready", "True", "", "")))
	if a.Sync != "Suspended" || a.SyncLight() != Amber || !strings.Contains(a.Explain(), "suspended") {
		t.Errorf("got %s %v %q", a.Sync, a.SyncLight(), a.Explain())
	}
}

func TestHelmReleaseFailed(t *testing.T) {
	u := fluxObj(map[string]any{
		"chart": map[string]any{"spec": map[string]any{"chart": "podinfo", "version": "6.x"}},
	},
		cond("Ready", "False", "UpgradeFailed", "Helm upgrade failed: timed out waiting for the condition"),
		cond("Released", "False", "UpgradeFailed", "Helm upgrade failed: timed out waiting for the condition"))
	u.Object["status"].(map[string]any)["history"] = []any{map[string]any{"chartVersion": "6.5.4"}}
	a := fromHelmRelease(u)

	if a.Kind != "HelmRelease" || a.Source != "podinfo (helm)" || a.Revision != "6.5.4" {
		t.Errorf("unexpected kind/source/revision: %q %q %q", a.Kind, a.Source, a.Revision)
	}
	if a.Sync != "Failed" || a.Health != "ReleaseFailed" || a.Overall() != Red {
		t.Errorf("want Failed/ReleaseFailed red, got %s/%s", a.Sync, a.Health)
	}
	// The same message on Ready and Released must appear only once.
	if got := a.Explain(); got != "UpgradeFailed: Helm upgrade failed: timed out waiting for the condition" {
		t.Errorf("Explain() = %q", got)
	}
}

func TestFluxRevisions(t *testing.T) {
	for in, want := range map[string]string{
		"main@sha1:f91dfdcc32b3a25e07cdede1d9569faf976555c2": "main@f91dfdc",
		"main/f91dfdcc32b3a25e07cdede1d9569faf976555c2":      "main@f91dfdc",
		"6.5.4": "6.5.4",
	} {
		if got := shortRevision(in); got != want {
			t.Errorf("shortRevision(%q) = %q, want %q", in, got, want)
		}
	}
}
