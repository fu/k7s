package pods

import (
	"fmt"
	"time"

	"github.com/fu/k7s/internal/kube"
	"github.com/fu/k7s/internal/resources"
)

// What a pod row shows. On narrow terminals pod names shrink first, then
// the utilisation percentages go.
var columns = []resources.Column{
	{Title: "NAME", Middle: true, Shrink: true},
	{Title: "READY"},
	{Title: "STATUS"},
	{Title: "RESTARTS"},
	{Title: "CPU", Right: true},
	{Title: "%CPU/R", Right: true, Drop: 1},
	{Title: "%CPU/L", Right: true, Drop: 3},
	{Title: "MEM", Right: true},
	{Title: "%MEM/R", Right: true, Drop: 2},
	{Title: "%MEM/L", Right: true, Drop: 4},
	{Title: "AGE"},
}

func (r *Resource) Columns() []resources.Column { return columns }

func (r *Resource) Rows() []resources.Row {
	now := time.Now()
	out := make([]resources.Row, len(r.order))
	for i, k := range r.order {
		out[i] = r.row(r.rows[k], now)
	}
	return out
}

func (r *Resource) row(rs *rowState, now time.Time) resources.Row {
	p := rs.row
	name := p.Name
	for _, local := range r.forwardsOf(p.Key()) {
		name += fmt.Sprintf(" ⇄:%d", local)
	}
	restarts := resources.Cell{Text: p.RestartsString()}
	if p.Restarts > 0 {
		restarts.Tone = resources.Warn
	}
	cells := map[string]resources.Cell{
		"NAME":  {Text: name},
		"READY": {Text: p.ReadyString()},
		// Just the light: the change log says what the state is and why.
		"STATUS":   {Text: "●", Tone: light(p)},
		"RESTARTS": restarts,
		"AGE":      {Text: kube.Age(p.Created)},
	}
	if rs.hasUsage {
		u := rs.usage
		cells["CPU"] = resources.Cell{Text: kube.CPU(u.CPU) + arrow(rs.cpuTrend), Tone: trendTone(rs.cpuTrend)}
		cells["MEM"] = resources.Cell{Text: kube.Mem(u.Mem) + arrow(rs.memTrend), Tone: trendTone(rs.memTrend)}
		cells["%CPU/R"] = pct(u.CPU, p.CPUReq, false)
		cells["%CPU/L"] = pct(u.CPU, p.CPULim, true)
		cells["%MEM/R"] = pct(u.Mem, p.MemReq, false)
		cells["%MEM/L"] = pct(u.Mem, p.MemLim, true)
	}
	if !rs.addedAt.IsZero() && now.Sub(rs.addedAt) < resources.FlashFor {
		c := cells["NAME"]
		c.Tone = resources.Fresh
		cells["NAME"] = c
	}
	for col, at := range rs.changed {
		if now.Sub(at) < resources.FlashFor {
			c := cells[col]
			c.Tone = resources.Flash
			cells[col] = c
		}
	}

	row := resources.Row{Key: p.Key(), Ref: p.ref(), State: p.state(), Struck: !rs.deletedAt.IsZero()}
	for _, c := range columns {
		cell, ok := cells[c.Title]
		if !ok {
			cell = resources.Cell{Text: "-", Tone: resources.Dim}
		}
		row.Cells = append(row.Cells, cell)
	}
	return row
}

// States a pod row can be in, for hiding rows.
func (r *Resource) States() []string { return []string{"running", "pending", "failing", "completed"} }

func (p Row) state() string {
	switch p.Health() {
	case Healthy:
		return "running"
	case Progressing:
		return "pending"
	case Finished:
		return "completed"
	}
	return "failing"
}

// light is the traffic light for a pod: green running, amber on its way
// (pending, creating, initializing), red terminating or failing.
func light(p Row) resources.Tone {
	if p.Status == "Terminating" {
		return resources.Bad
	}
	switch p.Health() {
	case Healthy:
		return resources.Good
	case Progressing:
		return resources.Busy
	case Finished:
		return resources.Dim
	}
	return resources.Alarm
}

// pct is usage as a percentage of a request or limit, warning when high.
func pct(v, of int64, isLimit bool) resources.Cell {
	p := kube.PctValue(v, of)
	c := resources.Cell{Text: kube.Pct(v, of)}
	switch {
	case p < 0:
		c.Tone = resources.Dim
	case isLimit && p >= 90, !isLimit && p >= 150:
		c.Tone = resources.Alarm
	case isLimit && p >= 70, !isLimit && p >= 100:
		c.Tone = resources.Warn
	}
	return c
}

func arrow(t int) string {
	switch t {
	case 1:
		return "↑"
	case -1:
		return "↓"
	}
	return " "
}

func trendTone(t int) resources.Tone {
	switch t {
	case 1:
		return resources.Bad
	case -1:
		return resources.Good
	}
	return resources.Plain
}
