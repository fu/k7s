package pvs

import (
	"time"

	"github.com/fu/k7s/internal/kube"
	"github.com/fu/k7s/internal/resources"
)

// What a volume row shows.
var columns = []resources.Column{
	{Title: "NAME", Middle: true, Shrink: true},
	{Title: "STATUS"},
	{Title: "CLAIM", Middle: true, Shrink: true},
	{Title: "CAPACITY", Right: true},
	{Title: "RECLAIM", Drop: 2},
	{Title: "CLASS", Drop: 1},
	{Title: "AGE"},
}

func (r *Resource) Columns() []resources.Column { return columns }

// Rows are the volumes that belong on the page.
func (r *Resource) Rows() []resources.Row {
	now := time.Now()
	var out []resources.Row
	for _, k := range r.shown() {
		rs := r.rows[k]
		v := rs.row
		status := resources.Cell{Text: "●", Tone: v.Light()}
		if v.Light() != resources.Good {
			status.Text = "● " + v.Phase
		}
		cells := map[string]resources.Cell{
			"NAME":     {Text: v.Name},
			"STATUS":   status,
			"CLAIM":    {Text: v.Claim},
			"CAPACITY": {Text: v.Capacity},
			"RECLAIM":  {Text: v.Reclaim, Tone: resources.Dim},
			"CLASS":    {Text: v.Class, Tone: resources.Dim},
			"AGE":      {Text: kube.Age(v.Created), Tone: resources.Dim},
		}
		if !rs.addedAt.IsZero() && now.Sub(rs.addedAt) < resources.FlashFor {
			cells["NAME"] = resources.Cell{Text: v.Name, Tone: resources.Fresh}
		}
		for col, at := range rs.changed {
			if now.Sub(at) < resources.FlashFor {
				c := cells[col]
				c.Tone = resources.Flash
				cells[col] = c
			}
		}
		row := resources.Row{Key: k, Ref: v.ref(), State: v.state(), Struck: !rs.deletedAt.IsZero()}
		for _, c := range columns {
			row.Cells = append(row.Cells, cells[c.Title])
		}
		out = append(out, row)
	}
	return out
}
