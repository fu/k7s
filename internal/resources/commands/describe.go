package commands

import (
	"context"

	"github.com/fu/k7s/internal/resources"
)

// Describe is "kubectl describe" of the row's object: kubectl's own
// describers, events included.
var Describe = resources.Command{
	Name: "describe",
	Run: func(t resources.Target) resources.Step {
		client := t.Host.Client()
		return viewer(t, "describe", func(context.Context) (string, error) {
			return client.Describe(t.Ref.Kind, t.Ref.Namespace, t.Ref.Name)
		})
	},
}
