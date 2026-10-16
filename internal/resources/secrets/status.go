package secrets

import (
	"fmt"
	"time"

	"github.com/fu/k7s/internal/resources"
)

// What the secrets can show in the status line; the config picks which.
func (r *Resource) Indicators() []resources.Indicator {
	return []resources.Indicator{
		{Name: "count", Cells: r.count},
		{Name: "errors", Cells: r.errors},
	}
}

// count: how many secrets.
func (r *Resource) count() []resources.Cell {
	return []resources.Cell{{Text: fmt.Sprint(len(r.order))}}
}

func (r *Resource) errors() []resources.Cell {
	if r.err != nil && time.Since(r.errAt) < 30*time.Second {
		return []resources.Cell{{Text: "watch error: " + r.err.Error(), Tone: resources.Bad}}
	}
	return nil
}
