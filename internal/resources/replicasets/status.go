package replicasets

import (
	"time"

	"github.com/fu/k7s/internal/resources"
)

// What the replicasets can show in the status line; the config picks which
// (by default only errors: the deployments say how the rollouts are).
func (r *Resource) Indicators() []resources.Indicator {
	return []resources.Indicator{
		{Name: "lights", Cells: r.lights},
		{Name: "errors", Cells: r.errors},
	}
}

// lights: how many replicasets, and how many are ready, on their way, failing
// (old revisions scaled to zero count as neither).
func (r *Resource) lights() []resources.Cell {
	var tones []resources.Tone
	for _, rs := range r.rows {
		if rs.deletedAt.IsZero() {
			tones = append(tones, rs.row.Light())
		}
	}
	return resources.Lights(tones)
}

func (r *Resource) errors() []resources.Cell {
	if r.err != nil && time.Since(r.errAt) < 30*time.Second {
		return []resources.Cell{{Text: "watch error: " + r.err.Error(), Tone: resources.Bad}}
	}
	return nil
}
