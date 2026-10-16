// Package commands are what a secret row can do: open.
package commands

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/fu/k7s/internal/resources"
	"github.com/fu/k7s/internal/resources/secrets"
)

// All are the secrets' commands.
func All() []resources.Command { return []resources.Command{Open} }

// Open shows a secret's keys; the values are fetched now and stay masked
// until revealed with r.
var Open = resources.Command{
	Name: "open",
	Run: func(t resources.Target) resources.Step {
		r, ok := t.Kind.(*secrets.Resource)
		if !ok {
			return nil
		}
		row, ok := r.Secret(t.Key)
		if !ok {
			return nil
		}
		client := t.Host.Client()
		return &resources.Viewer{
			Title: "secret " + row.Name + " │ " + row.Type,
			Stream: func(ctx context.Context, emit func(resources.Line)) error {
				ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
				defer cancel()
				data, err := secrets.Values(ctx, client, row.Namespace, row.Name)
				if err != nil {
					return err
				}
				lines(data, emit)
				return nil
			},
		}
	},
}

// lines lays out the keys, each with its value masked as a secret line.
func lines(data map[string][]byte, emit func(resources.Line)) {
	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := data[k]
		emit(resources.Line{Text: fmt.Sprintf("%s  (%d bytes)", k, len(v)), Tone: resources.Strong, Indent: 2})
		switch {
		case !utf8.Valid(v):
			emit(resources.Line{Text: fmt.Sprintf("<binary, %d bytes>", len(v)), Tone: resources.Dim, Indent: 4})
		case len(v) == 0:
			emit(resources.Line{Text: "(empty)", Tone: resources.Dim, Indent: 4})
		default:
			emit(resources.Line{Text: strings.TrimRight(string(v), "\n"), Indent: 4, Secret: true})
		}
	}
	if len(keys) == 0 {
		emit(resources.Line{Text: "no keys", Tone: resources.Dim, Indent: 2})
	}
}
