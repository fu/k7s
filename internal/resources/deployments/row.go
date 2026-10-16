package deployments

import (
	"fmt"
	"time"

	"github.com/fu/k7s/internal/kube"
	"github.com/fu/k7s/internal/resources"
)

// What a deployment row shows.
var columns = []resources.Column{
	{Title: "NAME", Middle: true, Shrink: true},
	{Title: "READY"},
	{Title: "UP-TO-DATE", Right: true},
	{Title: "AVAILABLE", Right: true},
	{Title: "STATUS"},
	{Title: "IMAGES", Shrink: true, Drop: 1},
	{Title: "AGE"},
}

func (r *Resource) Columns() []resources.Column { return columns }

// States a deployment row can be in, for hiding rows.
func (r *Resource) States() []string { return []string{"ready", "progressing", "stuck", "scaled-down"} }

func (d Row) state() string {
	switch d.Light() {
	case resources.Good:
		return "ready"
	case resources.Alarm:
		return "stuck"
	case resources.Dim:
		return "scaled-down"
	}
	return "progressing"
}

func (r *Resource) Rows() []resources.Row {
	now := time.Now()
	out := make([]resources.Row, len(r.order))
	for i, k := range r.order {
		rs := r.rows[k]
		d := rs.row
		cells := map[string]resources.Cell{
			"NAME":       {Text: d.Name},
			"READY":      {Text: d.ReadyString()},
			"UP-TO-DATE": {Text: fmt.Sprint(d.Updated)},
			"AVAILABLE":  {Text: fmt.Sprint(d.Available)},
			"STATUS":     {Text: "●", Tone: d.Light()},
			"IMAGES":     {Text: d.images(), Tone: resources.Dim},
			"AGE":        {Text: kube.Age(d.Created), Tone: resources.Dim},
		}
		if d.Paused {
			cells["STATUS"] = resources.Cell{Text: "● paused", Tone: resources.Busy}
		}
		if !rs.addedAt.IsZero() && now.Sub(rs.addedAt) < resources.FlashFor {
			cells["NAME"] = resources.Cell{Text: d.Name, Tone: resources.Fresh}
		}
		for col, at := range rs.changed {
			if now.Sub(at) < resources.FlashFor {
				c := cells[col]
				c.Tone = resources.Flash
				cells[col] = c
			}
		}
		row := resources.Row{Key: k, Ref: d.ref(), State: d.state(), Struck: !rs.deletedAt.IsZero()}
		for _, c := range columns {
			row.Cells = append(row.Cells, cells[c.Title])
		}
		out[i] = row
	}
	return out
}
