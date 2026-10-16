package deployments

import (
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/fu/k7s/internal/resources"
	"github.com/fu/k7s/internal/resources/restest"
)

func deployment(image string, ready int32, conds ...appsv1.DeploymentCondition) *appsv1.Deployment {
	three := int32(3)
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "web"},
		Spec: appsv1.DeploymentSpec{Replicas: &three, Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Image: image, EnvFrom: []corev1.EnvFromSource{
				{SecretRef: &corev1.SecretEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: "web-db"}}},
			}}},
		}}},
		Status: appsv1.DeploymentStatus{ReadyReplicas: ready, UpdatedReplicas: ready, AvailableReplicas: ready, Conditions: conds},
	}
}

func TestRolloutsAndStuckRolloutsAreLogged(t *testing.T) {
	h := &restest.Host{}
	r := New()
	r.host, r.rows = h, map[string]*rowState{}
	r.apply(fromDeployment(deployment("reg.io/web:1", 3)), false, true)
	if got := r.rows["ns/web"].row.Light(); got != resources.Good {
		t.Fatalf("3/3 ready is green, got %v", got)
	}

	r.apply(fromDeployment(deployment("reg.io/web:2", 2)), false, false)
	stuck := appsv1.DeploymentCondition{Type: appsv1.DeploymentProgressing, Status: corev1.ConditionFalse,
		Reason: "ProgressDeadlineExceeded", Message: `ReplicaSet "web-2" has timed out progressing.`}
	r.apply(fromDeployment(deployment("reg.io/web:2", 2, stuck)), false, false)

	if len(h.Changes) != 2 {
		t.Fatalf("want 2 changes, got %+v", h.Changes)
	}
	if d := h.Changes[0].Detail; !strings.HasPrefix(d, "rollout reg.io/web:1 → reg.io/web:2") || !strings.Contains(d, "ready 3 → 2") {
		t.Errorf("rollout: %q", d)
	}
	if d := h.Changes[1].Detail; !strings.Contains(d, "ProgressDeadlineExceeded") {
		t.Errorf("stuck: %q", d)
	}
	if got := r.rows["ns/web"].row.Light(); got != resources.Alarm {
		t.Errorf("a stuck rollout is red, got %v", got)
	}
}

func TestTemplateSecretsAreLinked(t *testing.T) {
	r := New()
	r.host, r.rows = &restest.Host{}, map[string]*rowState{}
	r.apply(fromDeployment(deployment("web:1", 3)), false, true)
	links := r.Links()
	want := resources.Link{From: resources.Ref{Kind: "Deployment", Namespace: "ns", Name: "web"}, To: resources.Ref{Kind: "Secret", Namespace: "ns", Name: "web-db"}}
	if len(links) != 1 || links[0] != want {
		t.Errorf("links = %+v", links)
	}
}
