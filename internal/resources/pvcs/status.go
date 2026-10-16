package pvcs

import (
	"time"

	"github.com/fu/k7s/internal/resources"
)

// What the claims can show in the status line; the config picks which.
func (r *Resource) Indicators() []resources.Indicator {
	return []resources.Indicator{
		{Name: "lights", Cells: r.lights},
		{Name: "errors", Cells: r.errors},
	}
}

// lights: how many claims, and how many are bound, pending, lost.
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
