package gitops

import (
	"fmt"
	"time"

	"github.com/fu/k7s/internal/resources"
)

// What gitops can show in the status line; the config picks which.
func (r *Resource) Indicators() []resources.Indicator {
	return []resources.Indicator{
		{Name: "light", Cells: r.light},
		{Name: "errors", Cells: r.errors},
	}
}

// light is one light for the whole deployment chain: an all-green pod list
// under a broken app (or a broken parent app) must not look fine. It says
// so when no app deploys what the page lists.
func (r *Resource) light() []resources.Cell {
	if !r.Available() {
		return nil
	}
	keys := r.responsible()
	if len(keys) == 0 {
		return []resources.Cell{{Text: "none here", Tone: resources.Dim}}
	}
	apps, parents := r.apps()
	worst := Grey
	for _, k := range keys {
		worst = max(worst, apps[k].Overall(), ChainLight(k, apps, parents))
	}
	return []resources.Cell{{Text: "●", Tone: resources.Light(int(worst))}, {Text: fmt.Sprintf(" %d apps", len(keys))}}
}

func (r *Resource) errors() []resources.Cell {
	if r.err != nil && time.Since(r.errAt) < 30*time.Second {
		return []resources.Cell{{Text: "watch error: " + r.err.Error(), Tone: resources.Bad}}
	}
	return nil
}
