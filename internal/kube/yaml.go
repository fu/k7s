package kube

import (
	"context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"
)

// YAML is "kubectl get <kind> <name> -o yaml", without managedFields. A
// secret's values are masked (their size kept): k7s never shows them
// unasked. ns is ignored for cluster-scoped kinds.
func (c *Client) YAML(ctx context.Context, kind, ns, name string) (string, error) {
	m, err := c.mappingFor(kind)
	if err != nil {
		return "", err
	}
	res := c.Dynamic.Resource(m.Resource)
	var get func() (map[string]any, error)
	if m.Scope.Name() == "namespace" {
		get = func() (map[string]any, error) {
			o, err := res.Namespace(ns).Get(ctx, name, metav1.GetOptions{})
			if err != nil {
				return nil, err
			}
			return o.Object, nil
		}
	} else {
		get = func() (map[string]any, error) {
			o, err := res.Get(ctx, name, metav1.GetOptions{})
			if err != nil {
				return nil, err
			}
			return o.Object, nil
		}
	}
	obj, err := get()
	if err != nil {
		return "", err
	}
	if md, ok := obj["metadata"].(map[string]any); ok {
		delete(md, "managedFields")
	}
	if kind == "Secret" {
		for _, field := range []string{"data", "stringData"} {
			if data, ok := obj[field].(map[string]any); ok {
				for k := range data {
					data[k] = "<masked>"
				}
			}
		}
	}
	out, err := yaml.Marshal(obj)
	return string(out), err
}
