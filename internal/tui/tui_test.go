package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/charmbracelet/lipgloss"
	"github.com/fu/k7s/internal/config"

	"github.com/fu/k7s/internal/kube"
	"github.com/fu/k7s/internal/resources"
)

// listing is a kind that only lists refs.
type listing struct {
	resources.Resource
	refs []resources.Ref
}

func (l listing) Listed() []resources.Ref { return l.refs }
func (l listing) Tag() string             { return "po" }
func (l listing) Objects() []string       { return []string{"Pod"} }

func TestChangesAreTaggedByKind(t *testing.T) {
	m := &model{kinds: []resources.Resource{listing{}}}
	m.addEvent(eventMsg{e: kube.Event{Namespace: "ns", Kind: "Pod", Name: "web-1", Type: "Warning", Reason: "Unhealthy", Message: "probe failed", At: time.Now()}})
	m.addEvent(eventMsg{e: kube.Event{Namespace: "ns", Kind: "Job", Name: "agent-x", Reason: "Completed", Message: "Job completed", At: time.Now()}})
	kindHost{m, listing{}}.Log(resources.Change{At: time.Now(), Kind: resources.Added, Object: "web-2"})

	var lines []string
	for _, c := range m.changes.entries {
		lines = append(lines, changeLine(c))
	}
	out := strings.Join(lines, "\n")
	// A pod event gets the pods' tag and the bare name; a Job (no section)
	// is "ev" and keeps its kind; what a kind logs gets its tag.
	for _, want := range []string{"po  WARNING  web-1", "ev  EVENT    job/agent-x", "po  ADDED    web-2"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestKubeEventsInChangeLog(t *testing.T) {
	m := &model{}
	m.addEvent(eventMsg{e: kube.Event{Namespace: "ns", Kind: "Pod", Name: "a", Type: "Warning", Reason: "BackOff", Message: "Back-off restarting", At: time.Now()}})
	m.addEvent(eventMsg{e: kube.Event{Namespace: "ns", Kind: "Pod", Name: "a", Type: "Warning", Reason: "Unhealthy", Message: "Liveness probe failed", Count: 3, At: time.Now()}})
	m.addEvent(eventMsg{e: kube.Event{Namespace: "ns", Kind: "Deployment", Name: "web", Type: "Normal", Reason: "ScalingReplicaSet", Message: "Scaled up to 2", At: time.Now().Add(-time.Hour), Initial: true}})
	ev := m.changes.entries
	if len(ev) != 2 {
		t.Fatalf("BackOff is skipped, the rest kept: %+v", ev)
	}
	// Kept in time order: the hour-old event (history) comes first.
	if ev[0].Kind != resources.Event || ev[0].Tag != "ev" || ev[0].Object != "deployment/web" || ev[1].Detail != "Unhealthy: Liveness probe failed (×3)" {
		t.Errorf("unexpected events: %+v", ev)
	}
}

func TestFilterShowsOnlyEventsAboutListedObjects(t *testing.T) {
	m := &model{info: Info{Selector: "app=web"}, kinds: []resources.Resource{listing{refs: []resources.Ref{
		{Kind: "Pod", Namespace: "ns", Name: "web-7d4-x"},
		{Kind: "Deployment", Namespace: "ns", Name: "web"},
	}}}}
	for _, e := range []kube.Event{
		{Namespace: "ns", Kind: "Pod", Name: "web-7d4-x", Reason: "Pulled"},
		{Namespace: "ns", Kind: "ReplicaSet", Name: "web-7d4", Reason: "SuccessfulCreate"},
		{Namespace: "ns", Kind: "Deployment", Name: "web", Reason: "ScalingReplicaSet"},
		{Namespace: "ns", Kind: "Pod", Name: "web-6c1-old", Reason: "Killing"}, // an earlier pod of web
		{Namespace: "ns", Kind: "Deployment", Name: "db", Reason: "ScalingReplicaSet"},
		{Namespace: "ns", Kind: "Pod", Name: "db-0", Reason: "Pulled"},
	} {
		m.addEvent(eventMsg{e: e})
	}
	out := m.changes.view(10, 120)
	if !strings.Contains(out, "changes (4)") || strings.Contains(out, "db") || !strings.Contains(out, "web-6c1-old") {
		t.Errorf("want the 4 events about web:\n%s", out)
	}
}

func TestJumpNumbers(t *testing.T) {
	var buf string
	var at time.Time
	typeNumber(&buf, &at, "1")
	if n := typeNumber(&buf, &at, "2"); n != 12 {
		t.Fatalf("typing 1 then 2 should give 12, got %d", n)
	}
	at = time.Now().Add(-2 * jumpWindow) // pause: next digit starts a new number
	if n := typeNumber(&buf, &at, "3"); n != 3 {
		t.Fatalf("after a pause, 3 should give 3, got %d", n)
	}
}

func TestTallEntryCanBeScrolledToItsFirstLine(t *testing.T) {
	c := changeLog{back: 25} // more than possible: the pane clamps it
	var items []string
	for i := 0; i < 17; i++ {
		items = append(items, "Deployment d"+fmt.Sprint(i)+" OutOfSync")
	}
	c.add(resources.Change{At: time.Now(), Kind: resources.State, Object: "shop", Detail: "auto-sync off", Items: items})
	c.add(resources.Change{At: time.Now(), Kind: resources.State, Object: "main-app"})
	out := c.view(10, 120)
	if c.back != 9 { // 19 lines in total, 10 visible
		t.Fatalf("back clamped to %d, want 9", c.back)
	}
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if len(lines) != 11 || !strings.Contains(lines[1], "STATE    shop") || !strings.Contains(lines[10], "d8 OutOfSync") {
		t.Fatalf("unexpected pane:\n%s", out)
	}
	if !strings.HasPrefix(lines[2], strings.Repeat(" ", changeIndent)+"· ") {
		t.Errorf("item line not indented under the entry: %q", lines[2])
	}
}

// Every kind toggles with its first letter, so those letters must not clash
// with each other, the scope menus, the page's own keys or the actions.
func TestPageKeysAreUnique(t *testing.T) {
	seen := map[string]string{}
	use := func(key, what string) {
		if other, dup := seen[key]; dup {
			t.Errorf("%s and %s share key %q", other, what, key)
		}
		seen[key] = what
	}
	for _, s := range scopes {
		use(s[:1], "menu "+s)
	}
	for _, k := range []string{"q", "a", "Q", "A", "j", "k", "G", "esc"} {
		use(k, "page key")
	}
	for _, k := range registry() {
		key := toggleKey(k)
		if !strings.Contains(k.Name(), key) {
			t.Errorf("section %s: key %q is not a letter of its name", k.Name(), key)
		}
		use(key, "section "+k.Name())
		if len(k.Tag()) > 3 {
			t.Errorf("section %s: tag %q is longer than 3 letters", k.Name(), k.Tag())
		}
	}
	// The default commands' keys must not be page keys (those would win);
	// sections may reuse each other's command keys.
	page := seen
	for section, cmds := range config.Default().Commands {
		used := map[string]string{}
		for name, keys := range cmds {
			for _, key := range strings.Fields(keys) {
				if other, ok := page[key]; ok {
					t.Errorf("command %s/%s: key %q is %s", section, name, key, other)
				}
				if other, ok := used[key]; ok {
					t.Errorf("commands %s and %s of %s share key %q", other, name, section, key)
				}
				used[key] = name
			}
		}
	}
}

func TestSectionsFollowResourceOrder(t *testing.T) {
	cfg := config.Default()
	var names []string
	for _, k := range ordered(cfg, registry()) {
		names = append(names, k.Name())
	}
	if got := strings.Join(names, " "); got != strings.Join(cfg.ResourceOrder, " ") {
		t.Errorf("default order %q, want %q", got, cfg.ResourceOrder)
	}
	cfg.ResourceOrder = []string{"pods", "gitops"}
	names = nil
	for _, k := range ordered(cfg, registry()) {
		names = append(names, k.Name())
	}
	if got := strings.Join(names, " "); got != "pods gitops deployments replicasets claims volumes secrets" {
		t.Errorf("pods and gitops on top: %q", got)
	}
}

func TestCommandsComeFromTheConfig(t *testing.T) {
	pods := podKind{rows: []resources.Row{{Key: "ns/a", Ref: resources.Ref{Kind: "Pod", Namespace: "ns", Name: "a"}}}}
	m := &model{kinds: []resources.Resource{pods}}
	m.page.cfg, m.page.selected = config.Default(), "pods/ns/a"

	var keys, hints []string
	for _, a := range m.actions() {
		keys = append(keys, a.key)
		if a.hint != "" {
			hints = append(hints, a.hint)
		}
	}
	if got := strings.Join(keys, " "); got != "l enter h w d" {
		t.Errorf("keys %q", got)
	}
	if got := strings.Join(hints, " "); got != "[l]ogs s[h]ell for[w]ard [d]escribe" {
		t.Errorf("hints %q", got)
	}

	// Remapped: shell on x (not a letter of shell), previous-logs on p is
	// not offered by default but can be.
	m.page.cfg.Commands["pods"] = map[string]string{"shell": "x", "previous-logs": "o"}
	hints = nil
	for _, a := range m.actions() {
		hints = append(hints, a.hint)
	}
	if got := strings.Join(hints, " "); got != "previ[o]us-logs [x] shell [d]escribe" {
		t.Errorf("remapped hints %q", got)
	}
}

func TestDividerMoves(t *testing.T) {
	m := &model{height: 40}
	key := func(k string) { m.pageKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}) }
	start := m.layout().changes // 12: a third of 40, at most 12
	key("Q")
	key("Q")
	if got := m.layout().changes; got != start+2 {
		t.Errorf("Q twice: %d lines, want %d", got, start+2)
	}
	for range 50 {
		key("Q")
	}
	if l := m.layout(); l.body != 3 {
		t.Errorf("the sections keep 3 lines, got %d", l.body)
	}
	key("A") // one step back down, right away
	if l := m.layout(); l.body != 4 {
		t.Errorf("A after the limit moves at once, body %d", l.body)
	}
	for range 50 {
		key("A")
	}
	if got := m.layout().changes; got != 2 {
		t.Errorf("the change log keeps 2 lines, got %d", got)
	}
}

func TestSectionToggles(t *testing.T) {
	m := &model{kinds: registry()}
	s := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")}
	byName := map[string]resources.Resource{}
	for _, k := range m.kinds {
		byName[k.Name()] = k
	}
	secrets, pods := byName["secrets"], byName["pods"]
	m.pageKey(s)
	if m.page.shown(secrets) || !m.page.shown(pods) {
		t.Fatal("s hides only the secrets section")
	}
	m.pageKey(s)
	if !m.page.shown(secrets) {
		t.Fatal("s again shows it again")
	}
}

func TestNarrowTableShrinksThenDrops(t *testing.T) {
	tb := table{
		cols: []resources.Column{
			{Title: "NAME", Middle: true, Shrink: true}, {Title: "CPU"}, {Title: "%CPU/R", Drop: 1},
		},
		rows: []resources.Row{{Key: "a", Cells: []resources.Cell{
			{Text: strings.Repeat("x", 40) + "-hash"}, {Text: "10m"}, {Text: "50%"},
		}}},
		number: true,
	}
	hdr, lines := tb.lines("", 50)
	if !strings.Contains(hdr, "%CPU/R") || !strings.Contains(lines[0], "…") || !strings.Contains(lines[0], "-hash") {
		t.Errorf("at 50 the name shrinks in the middle and all columns stay:\n%s\n%s", hdr, lines[0])
	}
	hdr, _ = tb.lines("", 40)
	if strings.Contains(hdr, "%CPU/R") || !strings.HasPrefix(hdr, "#") {
		t.Errorf("at 40 %%CPU/R is dropped, # stays: %q", hdr)
	}
	for _, l := range lines {
		if w := lipgloss.Width(l); w > 50 {
			t.Errorf("line wider than 50: %d", w)
		}
	}
}

func TestHintsDropTheBrackets(t *testing.T) {
	got := hints("s[h]ell  [esc] back")
	if strings.ContainsAny(got, "[]") || !strings.Contains(got, "h") || !strings.Contains(got, "esc") {
		t.Errorf("hints rendered %q", got)
	}
}

func TestWrapBreaksLongValues(t *testing.T) {
	long := strings.Repeat("QUJDREVGR0g=", 30) // a base64 blob without spaces
	lines := wrap(long, 60, 4, 4)
	if len(lines) < 6 {
		t.Fatalf("want the value spread over several lines, got %d", len(lines))
	}
	var joined string
	for _, l := range lines {
		if w := len([]rune(l)); w > 60 {
			t.Errorf("line wider than 60: %d", w)
		}
		if !strings.HasPrefix(l, "    ") {
			t.Errorf("continuation not indented: %q", l)
		}
		joined += strings.TrimSpace(l)
	}
	if joined != long {
		t.Error("wrapping changed the value")
	}
}

func TestViewerMasksSecretsUntilRevealed(t *testing.T) {
	s := &viewerScreen{v: &resources.Viewer{Title: "secret x"}, cancel: func() {}}
	s.update(viewerLineMsg{s, resources.Line{Text: "password", Indent: 2}})
	s.update(viewerLineMsg{s, resources.Line{Text: "hunter2", Indent: 4, Secret: true}})
	if out := s.view(80, 10); strings.Contains(out, "hunter2") || !strings.Contains(out, "••••") {
		t.Fatalf("value must be masked:\n%s", out)
	}
	s.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	if out := s.view(80, 10); !strings.Contains(out, "hunter2") {
		t.Fatalf("r reveals it:\n%s", out)
	}
}
