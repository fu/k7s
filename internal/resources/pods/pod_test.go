package pods

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestWorkload(t *testing.T) {
	yes := true
	pod := func(kind, name string, labels map[string]string) *corev1.Pod {
		return &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
			Labels:          labels,
			OwnerReferences: []metav1.OwnerReference{{Kind: kind, Name: name, Controller: &yes}},
		}}
	}
	for _, tc := range []struct {
		pod        *corev1.Pod
		kind, name string
	}{
		{pod("ReplicaSet", "web-7d4b9c6f5", map[string]string{"pod-template-hash": "7d4b9c6f5"}), "Deployment", "web"},
		{pod("ReplicaSet", "standalone-rs", nil), "ReplicaSet", "standalone-rs"},
		{pod("StatefulSet", "db", nil), "StatefulSet", "db"},
		{&corev1.Pod{}, "", ""},
	} {
		if k, n := workload(tc.pod); k != tc.kind || n != tc.name {
			t.Errorf("got %s/%s, want %s/%s", k, n, tc.kind, tc.name)
		}
	}
}
