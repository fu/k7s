package pvcs

import (
	"time"

	"github.com/fu/k7s/internal/kube"
	"github.com/fu/k7s/internal/resources"
)

// What a claim row shows.
var columns = []resources.Column{
	{Title: "NAME", Middle: true, Shrink: true},
	{Title: "STATUS"},
	{Title: "VOLUME", Middle: true, Shrink: true, Drop: 1},
	{Title: "CAPACITY", Right: true},
	{Title: "ACCESS"},
	{Title: "CLASS", Drop: 2},
	{Title: "AGE"},
}

func (r *Resource) Columns() []resources.Column { return columns }

func (r *Resource) Rows() []resources.Row {
	now := time.Now()
	out := make([]resources.Row, len(r.order))
	for i, k := range r.order {
		rs := r.rows[k]
		c := rs.row
		status := resources.Cell{Text: "●", Tone: c.Light()}
		if c.Light() != resources.Good {
			status.Text = "● " + c.Phase
		}
		cells := map[string]resources.Cell{
			"NAME":     {Text: c.Name},
			"STATUS":   status,
			"VOLUME":   {Text: c.Volume, Tone: resources.Dim},
			"CAPACITY": {Text: c.Capacity},
			"ACCESS":   {Text: c.Access, Tone: resources.Dim},
			"CLASS":    {Text: c.Class, Tone: resources.Dim},
			"AGE":      {Text: kube.Age(c.Created), Tone: resources.Dim},
		}
		if !rs.addedAt.IsZero() && now.Sub(rs.addedAt) < resources.FlashFor {
			cells["NAME"] = resources.Cell{Text: c.Name, Tone: resources.Fresh}
		}
		for col, at := range rs.changed {
			if now.Sub(at) < resources.FlashFor {
				cell := cells[col]
				cell.Tone = resources.Flash
				cells[col] = cell
			}
		}
		row := resources.Row{Key: k, Ref: c.ref(), State: c.state(), Struck: !rs.deletedAt.IsZero()}
		for _, col := range columns {
			row.Cells = append(row.Cells, cells[col.Title])
		}
		out[i] = row
	}
	return out
}
