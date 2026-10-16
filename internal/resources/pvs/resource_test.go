package pvs

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/fu/k7s/internal/resources"
	"github.com/fu/k7s/internal/resources/restest"
)

func volume(name, claimNS, claim string, phase corev1.PersistentVolumePhase) *corev1.PersistentVolume {
	return &corev1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: corev1.PersistentVolumeSpec{
			ClaimRef:                      &corev1.ObjectReference{Namespace: claimNS, Name: claim},
			PersistentVolumeReclaimPolicy: corev1.PersistentVolumeReclaimDelete,
		},
		Status: corev1.PersistentVolumeStatus{Phase: phase},
	}
}

func TestOnlyVolumesOfThePageAreShown(t *testing.T) {
	h := &restest.Host{NS: "shop"}
	r := New()
	r.host, r.rows = h, map[string]*rowState{}
	r.apply(fromVolume(volume("pv-a", "shop", "data-a", corev1.VolumeBound)), false, true)
	r.apply(fromVolume(volume("pv-b", "shop", "data-b", corev1.VolumeReleased)), false, true)
	r.apply(fromVolume(volume("pv-x", "other", "data-x", corev1.VolumeBound)), false, true)

	if rows := r.Rows(); len(rows) != 2 || rows[0].Key != "pv-a" || rows[1].State != "released" {
		t.Fatalf("no filter: %+v", rows)
	}

	// With a filter, only the volumes of listed claims.
	h.Filter = "app=a"
	h.Refs = []resources.Ref{{Kind: "PersistentVolumeClaim", Namespace: "shop", Name: "data-a"}}
	if rows := r.Rows(); len(rows) != 1 || rows[0].Key != "pv-a" {
		t.Fatalf("filtered: %+v", rows)
	}

	// Changes of other namespaces' volumes stay out of the log.
	r.apply(fromVolume(volume("pv-x", "other", "data-x", corev1.VolumeReleased)), false, false)
	if len(h.Changes) != 1 || h.Changes[0].Show() {
		t.Errorf("pv-x is not on the page: %+v", h.Changes)
	}
}
