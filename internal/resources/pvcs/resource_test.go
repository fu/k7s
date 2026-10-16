package pvcs

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/fu/k7s/internal/resources"
	"github.com/fu/k7s/internal/resources/restest"
)

func claim(phase corev1.PersistentVolumeClaimPhase, volume, size string) *corev1.PersistentVolumeClaim {
	class := "managed"
	return &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "data-db-0"},
		Spec: corev1.PersistentVolumeClaimSpec{
			VolumeName: volume, StorageClassName: &class,
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
		},
		Status: corev1.PersistentVolumeClaimStatus{
			Phase:    phase,
			Capacity: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse(size)},
		},
	}
}

func TestBindingAndResizingAreLogged(t *testing.T) {
	h := &restest.Host{}
	r := New()
	r.host, r.rows = h, map[string]*rowState{}
	r.apply(fromClaim(claim(corev1.ClaimPending, "", "10Gi")), false, true)
	if got := r.rows["ns/data-db-0"].row; got.Light() != resources.Busy || got.Access != "RWO" {
		t.Fatalf("pending claim: %+v", got)
	}
	r.apply(fromClaim(claim(corev1.ClaimBound, "pvc-3f2a", "10Gi")), false, false)
	r.apply(fromClaim(claim(corev1.ClaimBound, "pvc-3f2a", "20Gi")), false, false)

	if len(h.Changes) != 2 || h.Changes[0].Detail != "status Pending → Bound (volume pvc-3f2a)" || h.Changes[1].Detail != "resized 10Gi → 20Gi" {
		t.Fatalf("changes: %+v", h.Changes)
	}
	links := r.Links()
	if len(links) != 1 || links[0].To != (resources.Ref{Kind: "PersistentVolume", Name: "pvc-3f2a"}) {
		t.Errorf("links: %+v", links)
	}
}
