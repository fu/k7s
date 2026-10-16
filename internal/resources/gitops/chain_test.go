package gitops

import "testing"

func TestParentChainLight(t *testing.T) {
	apps := map[string]App{
		"root": {Kind: "Application", Namespace: "argocd", Name: "root", SyncL: Green, HealthL: Red,
			Manages: []Ref{{Kind: "Application", Namespace: "argocd", Name: "shop"}}},
		"shop": {Kind: "Application", Namespace: "argocd", Name: "shop", SyncL: Green, HealthL: Green,
			Manages: []Ref{{Kind: "Deployment", Namespace: "shop", Name: "web"}}},
		"other": {Kind: "Application", Namespace: "argocd", Name: "other", SyncL: Green, HealthL: Green},
	}
	parents := Parents(apps)
	if parents["shop"] != "root" || len(parents) != 1 {
		t.Fatalf("parents = %v", parents)
	}
	if got := ChainLight("shop", apps, parents); got != Red {
		t.Errorf("a green app under a red parent must show red, got %v", got)
	}
	if got := ChainLight("other", apps, parents); got != Grey {
		t.Errorf("no parent: want Grey, got %v", got)
	}
	if !apps["shop"].ManagesRef(Ref{Kind: "Deployment", Namespace: "shop", Name: "web"}) {
		t.Error("shop should manage Deployment shop/web")
	}
}

func TestChainLightSurvivesCycles(t *testing.T) {
	apps := map[string]App{
		"a": {Kind: "Application", Name: "a", SyncL: Amber, Manages: []Ref{{Kind: "Application", Name: "b"}}},
		"b": {Kind: "Application", Name: "b", SyncL: Green, Manages: []Ref{{Kind: "Application", Name: "a"}}},
	}
	if got := ChainLight("a", apps, Parents(apps)); got != Green {
		t.Errorf("got %v", got)
	}
}
