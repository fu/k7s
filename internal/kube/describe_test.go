package kube

import (
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	discoveryfake "k8s.io/client-go/discovery/fake"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
)

func TestMappingFromDiscovery(t *testing.T) {
	cs := fake.NewClientset(&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "web"}})
	cs.Discovery().(*discoveryfake.FakeDiscovery).Resources = []*metav1.APIResourceList{
		{GroupVersion: "apps/v1", APIResources: []metav1.APIResource{
			{Name: "deployments", Kind: "Deployment", Namespaced: true},
			{Name: "deployments/scale", Kind: "Scale", Namespaced: true},
		}},
		{GroupVersion: "v1", APIResources: []metav1.APIResource{{Name: "persistentvolumes", Kind: "PersistentVolume"}}},
	}
	c := &Client{Kube: cs, Config: &rest.Config{}}

	m, err := c.mappingFor("Deployment")
	if err != nil || m.Resource.Resource != "deployments" || m.GroupVersionKind.Group != "apps" || m.Scope.Name() != "namespace" {
		t.Fatalf("Deployment: %+v %v", m, err)
	}
	if m, err := c.mappingFor("PersistentVolume"); err != nil || m.Scope.Name() != "root" {
		t.Errorf("PersistentVolume is cluster-scoped: %+v %v", m, err)
	}
	if _, err := c.mappingFor("Scale"); err == nil || !strings.Contains(err.Error(), "no kind") {
		t.Errorf("subresources are not kinds: %v", err)
	}
}
