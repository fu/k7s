package secrets

import (
	"fmt"

	"github.com/fu/k7s/internal/kube"
	"github.com/fu/k7s/internal/resources"
)

// What a secret row shows (never its values).
var columns = []resources.Column{
	{Title: "NAME", Middle: true, Shrink: true},
	{Title: "TYPE"},
	{Title: "KEYS", Right: true},
	{Title: "AGE"},
}

func (r *Resource) Columns() []resources.Column { return columns }

func (r *Resource) Rows() []resources.Row {
	out := make([]resources.Row, len(r.order))
	for i, k := range r.order {
		s := r.rows[k]
		out[i] = resources.Row{Key: k, Ref: resources.Ref{Kind: "Secret", Namespace: s.Namespace, Name: s.Name}, Cells: []resources.Cell{
			{Text: s.Name},
			{Text: s.Type, Tone: resources.Dim},
			{Text: fmt.Sprint(s.Keys)},
			{Text: kube.Age(s.Created), Tone: resources.Dim},
		}}
	}
	return out
}
