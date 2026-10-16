package tui

import (
	"testing"

	"github.com/fu/k7s/internal/resources"
)

func TestLinkedFollowsBothWaysButNotSideways(t *testing.T) {
	ref := func(kind, name string) resources.Ref { return resources.Ref{Kind: kind, Namespace: "shop", Name: name} }
	app, dep := ref("Application", "web"), ref("Deployment", "web")
	pod1, pod2 := ref("Pod", "web-1"), ref("Pod", "web-2")
	tls, db := ref("Secret", "tls"), ref("Secret", "db")
	links := []resources.Link{
		{From: app, To: dep},
		{From: dep, To: pod1}, {From: dep, To: pod2},
		{From: pod1, To: tls}, {From: pod2, To: db},
	}
	for _, tc := range []struct {
		sel  resources.Ref
		want []resources.Ref
	}{
		{app, []resources.Ref{dep, pod1, pod2, tls, db}}, // the app: its pods and their secrets
		{pod1, []resources.Ref{dep, app, tls}},           // a pod: its app and its secrets, not web-2
		{db, []resources.Ref{pod2, dep, app}},            // a secret: who uses it, up to the app
		{ref("Secret", "unused"), nil},                   // nothing linked
	} {
		got := linked(tc.sel, links)
		if len(got) != len(tc.want) {
			t.Errorf("%s: got %v, want %v", tc.sel.Name, got, tc.want)
			continue
		}
		for _, w := range tc.want {
			if !got[w] {
				t.Errorf("%s: %s not linked", tc.sel.Name, w.Name)
			}
		}
	}
}
