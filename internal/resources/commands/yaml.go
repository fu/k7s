package commands

import (
	"context"

	"github.com/fu/k7s/internal/resources"
)

// YAML is "kubectl get -o yaml" of the row's object, without managedFields;
// secret values are masked.
var YAML = resources.Command{
	Name: "yaml",
	Run: func(t resources.Target) resources.Step {
		client := t.Host.Client()
		return viewer(t, "yaml", func(ctx context.Context) (string, error) {
			return client.YAML(ctx, t.Ref.Kind, t.Ref.Namespace, t.Ref.Name)
		})
	},
}
