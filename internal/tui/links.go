package tui

import "github.com/fu/k7s/internal/resources"

// The link view: everything linked to the selected row is highlighted,
// across sections. Links are followed both ways — down to what it leads to
// (app → Deployment → pods → secrets) and up to what leads to it (secret ←
// pods ← Deployment ← app) — but not sideways: selecting a pod does not
// light up its sibling pods.

// linked returns the objects linked to sel (sel itself not included).
func linked(sel resources.Ref, links []resources.Link) map[resources.Ref]bool {
	out := map[resources.Ref]bool{}
	if sel == (resources.Ref{}) {
		return out
	}
	down, up := map[resources.Ref][]resources.Ref{}, map[resources.Ref][]resources.Ref{}
	for _, l := range links {
		down[l.From] = append(down[l.From], l.To)
		up[l.To] = append(up[l.To], l.From)
	}
	for _, next := range []map[resources.Ref][]resources.Ref{down, up} {
		seen := map[resources.Ref]bool{sel: true}
		queue := []resources.Ref{sel}
		for len(queue) > 0 {
			r := queue[0]
			queue = queue[1:]
			for _, n := range next[r] {
				if !seen[n] {
					seen[n], out[n] = true, true
					queue = append(queue, n)
				}
			}
		}
	}
	return out
}

// pageLinks collects the links of every kind that has some.
func (m *model) pageLinks() []resources.Link {
	var out []resources.Link
	for _, k := range m.kinds {
		if l, ok := k.(resources.Linker); ok {
			out = append(out, l.Links()...)
		}
	}
	return out
}
