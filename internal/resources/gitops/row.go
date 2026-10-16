package gitops

import (
	"slices"
	"time"

	"github.com/fu/k7s/internal/kube"
	"github.com/fu/k7s/internal/resources"
)

// What an app row shows. PARENT is the app that deploys this one, lit by the
// worst light up that chain, so a green app under a broken parent does not
// look fine. Flux mixes Kustomizations and HelmReleases, so it gets a KIND.
func (r *Resource) Columns() []resources.Column {
	cols := []resources.Column{
		{Title: "SYNC"}, {Title: "HEALTH"},
		{Title: "NAME", Middle: true},
		{Title: "DEST"},
		{Title: "PARENT", Middle: true, Shrink: true},
		{Title: "SOURCE", Middle: true, Shrink: true},
		{Title: "REV"},
		{Title: "SYNCED"},
	}
	if r.engine == Flux {
		cols = slices.Insert(cols, 2, resources.Column{Title: "KIND"})
	}
	return cols
}

// Rows are the responsible apps.
func (r *Resource) Rows() []resources.Row {
	apps, parents := r.apps()
	now := time.Now()
	var out []resources.Row
	for _, k := range r.responsible() {
		row := r.rows[k]
		a := row.app
		sync := resources.Cell{Text: "●", Tone: resources.Light(int(a.SyncLight()))}
		health := resources.Cell{Text: "●", Tone: resources.Light(int(a.HealthLight()))}
		if now.Sub(row.syncChanged) < resources.FlashFor {
			sync.Tone = resources.Flash
		}
		if now.Sub(row.healthChanged) < resources.FlashFor {
			health.Tone = resources.Flash
		}
		synced := resources.Cell{Text: "-", Tone: resources.Dim}
		if !a.SyncedAt.IsZero() {
			synced.Text = kube.Age(a.SyncedAt)
		}
		var parent resources.Cell
		if p, ok := parents[k]; ok {
			parent = resources.Cell{Text: "● " + apps[p].Name, Tone: resources.Light(int(ChainLight(k, apps, parents)))}
		}
		cells := []resources.Cell{
			sync, health,
			{Text: a.Name},
			{Text: a.DestNS, Tone: resources.Dim},
			parent,
			{Text: a.Source, Tone: resources.Dim},
			{Text: a.Revision, Tone: resources.Dim},
			synced,
		}
		if r.engine == Flux {
			cells = slices.Insert(cells, 2, resources.Cell{Text: a.Kind, Tone: resources.Dim})
		}
		out = append(out, resources.Row{Key: k, Cells: cells, Struck: !row.deletedAt.IsZero()})
	}
	return out
}
