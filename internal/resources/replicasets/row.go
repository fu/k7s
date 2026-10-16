package replicasets

import (
	"fmt"
	"time"

	"github.com/fu/k7s/internal/kube"
	"github.com/fu/k7s/internal/resources"
)

// What a replicaset row shows; old revisions scaled to zero are dimmed.
var columns = []resources.Column{
	{Title: "NAME", Middle: true, Shrink: true},
	{Title: "REV", Right: true},
	{Title: "DESIRED", Right: true},
	{Title: "CURRENT", Right: true},
	{Title: "READY", Right: true},
	{Title: "STATUS"},
	{Title: "AGE"},
}

func (r *Resource) Columns() []resources.Column { return columns }

func (r *Resource) Rows() []resources.Row {
	now := time.Now()
	out := make([]resources.Row, len(r.order))
	for i, k := range r.order {
		rs := r.rows[k]
		s := rs.row
		tone := resources.Plain
		if s.Light() == resources.Dim {
			tone = resources.Dim
		}
		cells := map[string]resources.Cell{
			"NAME":    {Text: s.Name, Tone: tone},
			"REV":     {Text: s.Revision, Tone: resources.Dim},
			"DESIRED": {Text: fmt.Sprint(s.Desired), Tone: tone},
			"CURRENT": {Text: fmt.Sprint(s.Current), Tone: tone},
			"READY":   {Text: fmt.Sprint(s.Ready), Tone: tone},
			"STATUS":  {Text: "●", Tone: s.Light()},
			"AGE":     {Text: kube.Age(s.Created), Tone: resources.Dim},
		}
		if !rs.addedAt.IsZero() && now.Sub(rs.addedAt) < resources.FlashFor {
			cells["NAME"] = resources.Cell{Text: s.Name, Tone: resources.Fresh}
		}
		for col, at := range rs.changed {
			if now.Sub(at) < resources.FlashFor {
				c := cells[col]
				c.Tone = resources.Flash
				cells[col] = c
			}
		}
		row := resources.Row{Key: k, Ref: s.ref(), Struck: !rs.deletedAt.IsZero()}
		for _, c := range columns {
			row.Cells = append(row.Cells, cells[c.Title])
		}
		out[i] = row
	}
	return out
}
