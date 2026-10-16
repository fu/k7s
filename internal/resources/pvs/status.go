package pvs

import (
	"time"

	"github.com/fu/k7s/internal/resources"
)

// What the volumes can show in the status line; the config picks which (by
// default only errors: the claims say how storage is).
func (r *Resource) Indicators() []resources.Indicator {
	return []resources.Indicator{
		{Name: "lights", Cells: r.lights},
		{Name: "errors", Cells: r.errors},
	}
}

// lights: how many volumes are on the page, and how many are bound,
// available or released, failed.
func (r *Resource) lights() []resources.Cell {
	var tones []resources.Tone
	for _, k := range r.shown() {
		tones = append(tones, r.rows[k].row.Light())
	}
	return resources.Lights(tones)
}

func (r *Resource) errors() []resources.Cell {
	if r.err != nil && time.Since(r.errAt) < 30*time.Second {
		return []resources.Cell{{Text: "watch error: " + r.err.Error(), Tone: resources.Bad}}
	}
	return nil
}
