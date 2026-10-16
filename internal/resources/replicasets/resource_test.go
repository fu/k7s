package replicasets

import (
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/fu/k7s/internal/resources"
	"github.com/fu/k7s/internal/resources/restest"
)

func replicaSet(name, rev string, replicas int32) *appsv1.ReplicaSet {
	yes := true
	return &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "ns", Name: name,
			Annotations:     map[string]string{"deployment.kubernetes.io/revision": rev},
			OwnerReferences: []metav1.OwnerReference{{Kind: "Deployment", Name: "web", Controller: &yes}},
		},
		Spec:   appsv1.ReplicaSetSpec{Replicas: &replicas},
		Status: appsv1.ReplicaSetStatus{Replicas: replicas, ReadyReplicas: replicas},
	}
}

func TestRolloutRevisions(t *testing.T) {
	h := &restest.Host{}
	r := New()
	r.host, r.rows = h, map[string]*rowState{}
	r.apply(fromReplicaSet(replicaSet("web-1", "1", 3)), false, true)
	r.apply(fromReplicaSet(replicaSet("web-2", "2", 1)), false, false) // the rollout starts
	r.apply(fromReplicaSet(replicaSet("web-1", "1", 0)), false, false) // the old revision goes

	if len(h.Changes) != 2 || h.Changes[0].Detail != "revision 2, 1 replicas" || h.Changes[1].Detail != "scaled 3 → 0" {
		t.Fatalf("changes: %+v", h.Changes)
	}
	if got := r.rows["ns/web-1"].row.Light(); got != resources.Dim {
		t.Errorf("an old revision is dim, got %v", got)
	}
	links := r.Links()
	if len(links) != 2 || links[0].From != (resources.Ref{Kind: "Deployment", Namespace: "ns", Name: "web"}) {
		t.Errorf("links = %+v", links)
	}
}
