package kube

import (
	"fmt"
	"strings"
	"sync"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/kubectl/pkg/describe"
)

// Describe is "kubectl describe <kind> <name>": the same text, from
// kubectl's own describers (built-in kinds) or its generic one (CRDs such
// as Argo CD Applications). ns is empty for cluster-scoped kinds.
func (c *Client) Describe(kind, ns, name string) (string, error) {
	mapping, err := c.mappingFor(kind)
	if err != nil {
		return "", err
	}
	d, ok := describe.DescriberFor(mapping.GroupVersionKind.GroupKind(), c.Config)
	if !ok {
		if d, ok = describe.GenericDescriberFor(mapping, c.Config); !ok {
			return "", fmt.Errorf("cannot describe %s", kind)
		}
	}
	if mapping.Scope.Name() != meta.RESTScopeNameNamespace {
		ns = ""
	}
	return d.Describe(ns, name, describe.DescriberSettings{ShowEvents: true, ChunkSize: 500})
}

// kinds maps a kind ("Deployment") to its REST mapping, from the cluster's
// preferred API versions; read once per client.
type kinds struct {
	once   sync.Once
	byKind map[string]*meta.RESTMapping
	err    error
}

func (c *Client) mappingFor(kind string) (*meta.RESTMapping, error) {
	k := &c.kinds
	k.once.Do(func() {
		lists, err := discovery.ServerPreferredResources(c.Kube.Discovery())
		if len(lists) == 0 && err != nil {
			k.err = err
			return
		}
		k.byKind = map[string]*meta.RESTMapping{}
		for _, list := range lists {
			gv, err := schema.ParseGroupVersion(list.GroupVersion)
			if err != nil {
				continue
			}
			for _, r := range list.APIResources {
				if _, seen := k.byKind[r.Kind]; seen || r.Name == "" || strings.Contains(r.Name, "/") {
					continue // first (preferred) wins; skip subresources
				}
				scope := meta.RESTScopeRoot
				if r.Namespaced {
					scope = meta.RESTScopeNamespace
				}
				k.byKind[r.Kind] = &meta.RESTMapping{
					Resource:         gv.WithResource(r.Name),
					GroupVersionKind: gv.WithKind(r.Kind),
					Scope:            scope,
				}
			}
		}
	})
	if k.err != nil {
		return nil, k.err
	}
	if m, ok := k.byKind[kind]; ok {
		return m, nil
	}
	return nil, fmt.Errorf("the cluster has no kind %s", kind)
}
