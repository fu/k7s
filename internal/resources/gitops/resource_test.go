package gitops

import (
	"testing"

	"github.com/fu/k7s/internal/resources/restest"
)

func TestChangesSayWhatBrokeAndWhatGotFixed(t *testing.T) {
	h := &restest.Host{NS: "shop"}
	r := &Resource{host: h, rows: map[string]*appRow{}, detected: true, engine: Argo}
	app := App{Kind: "Application", Name: "web", DestNS: "shop", Sync: "Synced", Health: "Healthy", SyncL: Green, HealthL: Green}
	r.apply(Event{Type: Added, App: app, Initial: true})

	broken := app
	broken.Health, broken.HealthL = "Degraded", Red
	broken.Problems = []string{"Deployment web Degraded: exceeded its progress deadline"}
	broken.Why = []string{"Deployment web Degraded"}
	// Getting worse is logged once k7s has looked at what the app manages.
	if cmd := r.apply(Event{Type: Modified, App: broken}); cmd != nil {
		r.Update(cmd())
	}

	r.apply(Event{Type: Modified, App: app})

	if len(h.Changes) != 2 {
		t.Fatalf("want 2 changes, got %+v", h.Changes)
	}
	got := h.Changes[0]
	if got.Object != "web" || got.Tag != "ap" || got.Detail != "health Healthy → Degraded, Deployment web Degraded" ||
		len(got.Items) != 1 || got.Items[0] != broken.Problems[0] || len(got.State) != 0 {
		t.Errorf("breaking change: %+v", got)
	}
	if fixed := h.Changes[1]; len(fixed.Items) != 1 || fixed.Items[0] != "fixed: "+broken.Problems[0] {
		t.Errorf("fixing change: %+v", fixed)
	}
	if !h.Changes[0].Show() {
		t.Error("the app deploys into the page's namespace, so its changes show")
	}
}

func TestChildAppsExplainADegradedParent(t *testing.T) {
	child := App{Kind: "Application", Namespace: "argocd", Name: "shop", Sync: "Synced", Health: "Degraded",
		SyncL: Green, HealthL: Red, Why: []string{"auto-sync off"}}
	parent := App{Kind: "Application", Namespace: "argocd", Name: "main-app", Sync: "Synced", Health: "Degraded",
		Manages: []Ref{{Kind: "Application", Namespace: "argocd", Name: "shop"}, {Kind: "ConfigMap", Name: "x"}}}
	apps := map[string]App{child.Key(): child, parent.Key(): parent}
	got := diagnose(t.Context(), nil, parent, apps)
	if len(got) != 1 || got[0] != "Application shop Synced Degraded: auto-sync off" {
		t.Errorf("diagnosis: %q", got)
	}
}
