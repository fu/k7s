// Package commands are the commands every kind's rows offer: describe and
// yaml. Kind-specific ones live in the kind's own commands folder.
package commands

import (
	"context"
	"strings"

	"github.com/fu/k7s/internal/resources"
)

// All are the commands for every kind, in hint order.
func All() []resources.Command { return []resources.Command{Describe, YAML} }

// text turns command output into viewer lines (tabs as spaces).
func text(out string, emit func(resources.Line)) {
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		emit(resources.Line{Text: strings.ReplaceAll(line, "\t", "    ")})
	}
}

// viewer shows the output of fetch for the target, titled "<what> <kind> <name>".
func viewer(t resources.Target, what string, fetch func(ctx context.Context) (string, error)) resources.Step {
	if t.Ref.Kind == "" {
		return nil
	}
	return &resources.Viewer{
		Title: what + " " + strings.ToLower(t.Ref.Kind) + " " + t.Ref.Name,
		Stream: func(ctx context.Context, emit func(resources.Line)) error {
			out, err := fetch(ctx)
			if err != nil {
				return err
			}
			text(out, emit)
			return nil
		},
	}
}
