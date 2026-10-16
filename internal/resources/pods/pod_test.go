package pods

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/fu/k7s/internal/resources"
)

func TestSecretsUsed(t *testing.T) {
	pod := &corev1.Pod{Spec: corev1.PodSpec{
		Volumes: []corev1.Volume{
			{VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: "tls"}}},
			{VolumeSource: corev1.VolumeSource{Projected: &corev1.ProjectedVolumeSource{Sources: []corev1.VolumeProjection{
				{Secret: &corev1.SecretProjection{LocalObjectReference: corev1.LocalObjectReference{Name: "projected"}}},
			}}}},
		},
		InitContainers: []corev1.Container{{EnvFrom: []corev1.EnvFromSource{
			{SecretRef: &corev1.SecretEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: "init-env"}}},
		}}},
		Containers: []corev1.Container{{Env: []corev1.EnvVar{
			{Name: "PW", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "db"}, Key: "pw"}}},
			{Name: "PLAIN", Value: "x"},
		}}},
		ImagePullSecrets: []corev1.LocalObjectReference{{Name: "registry"}, {Name: "tls"}},
	}}
	got := secretsUsed(pod)
	want := []string{"db", "init-env", "projected", "registry", "tls"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestJobOwners(t *testing.T) {
	ref := func(kind, name string) resources.Ref { return resources.Ref{Kind: kind, Namespace: "ns", Name: name} }
	if l := jobOwners(ref("Job", "backup-29012345")); len(l) != 1 || l[0].From != ref("CronJob", "backup") {
		t.Errorf("cron job: %+v", l)
	}
	if l := jobOwners(ref("Job", "agent-zlvsj")); len(l) != 1 || l[0].From != ref("ScaledJob", "agent") {
		t.Errorf("scaled job: %+v", l)
	}
	if l := jobOwners(ref("Deployment", "agent-zlvsj")); l != nil {
		t.Errorf("only jobs: %+v", l)
	}
}

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
