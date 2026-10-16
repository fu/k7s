package pods

import (
	"fmt"
	"time"

	"github.com/fu/k7s/internal/resources"
)

// What the pods can show in the status line; the config picks which.
func (r *Resource) Indicators() []resources.Indicator {
	return []resources.Indicator{
		{Name: "lights", Cells: r.lights},
		{Name: "forwards", Cells: r.forwarded},
		{Name: "metrics", Cells: r.metricsProblem},
		{Name: "errors", Cells: r.errors},
	}
}

// lights: how many pods, and how many are running, on their way, failing.
func (r *Resource) lights() []resources.Cell {
	var healthy, progressing, failing int
	for _, rs := range r.rows {
		if !rs.deletedAt.IsZero() {
			continue
		}
		switch rs.row.Health() {
		case Healthy:
			healthy++
		case Progressing:
			progressing++
		case Failing:
			failing++
		}
	}
	return []resources.Cell{
		{Text: fmt.Sprintf("%d ", len(r.order))},
		{Text: fmt.Sprintf("●%d ", healthy), Tone: resources.Good},
		{Text: fmt.Sprintf("●%d ", progressing), Tone: resources.Busy},
		{Text: fmt.Sprintf("●%d", failing), Tone: resources.Bad},
	}
}

// forwarded: how many port-forwards are running.
func (r *Resource) forwarded() []resources.Cell {
	if n := len(r.forwards); n > 0 {
		return []resources.Cell{{Text: fmt.Sprintf("⇄ %d forwarded", n), Tone: resources.Info}}
	}
	return nil
}

// metricsProblem: metrics-server cannot be reached, so CPU/MEM are missing.
func (r *Resource) metricsProblem() []resources.Cell {
	if r.metricsErr != nil {
		return []resources.Cell{{Text: "metrics unavailable", Tone: resources.Bad}}
	}
	return nil
}

func (r *Resource) errors() []resources.Cell {
	if r.watchErr != nil && time.Since(r.watchErrAt) < 30*time.Second {
		return []resources.Cell{{Text: "watch error: " + r.watchErr.Error(), Tone: resources.Bad}}
	}
	return nil
}
