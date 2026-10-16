package gitops

import (
	"context"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/fu/k7s/internal/kube"
	"github.com/fu/k7s/internal/resources"
)

// testHost records what a kind logs.
type testHost struct{ log []resources.Change }

func (h *testHost) Context() context.Context { return context.Background() }
func (h *testHost) Client() *kube.Client     { return nil }
func (h *testHost) Namespace() string        { return "shop" }
func (h *testHost) Selector() string         { return "" }
func (h *testHost) Refresh() time.Duration   { return 5 * time.Second }
func (h *testHost) Send(tea.Msg)             {}
func (h *testHost) Log(c resources.Change)   { h.log = append(h.log, c) }
func (h *testHost) Notice(string)            {}
func (h *testHost) Listed() []resources.Ref  { return nil }
func (h *testHost) Pulse(time.Time)          {}

func TestChangesSayWhatBrokeAndWhatGotFixed(t *testing.T) {
	h := &testHost{}
	r := &Resource{host: h, rows: map[string]*appRow{}, detected: true, engine: Argo}
	app := App{Kind: "Application", Name: "web", DestNS: "shop", Sync: "Synced", Health: "Healthy", SyncL: Green, HealthL: Green}
	r.apply(Event{Type: Added, App: app, Initial: true})

	broken := app
	broken.Health, broken.HealthL = "Degraded", Red
	broken.Problems = []string{"Deployment web Degraded: exceeded its progress deadline"}
	broken.Why = []string{"Deployment web Degraded"}
	r.apply(Event{Type: Modified, App: broken})

	r.apply(Event{Type: Modified, App: app})

	if len(h.log) != 2 {
		t.Fatalf("want 2 changes, got %+v", h.log)
	}
	got := h.log[0]
	if got.Object != "app/web" || got.Detail != "health Healthy → Degraded, Deployment web Degraded" ||
		len(got.Items) != 1 || got.Items[0] != broken.Problems[0] || len(got.State) != 0 {
		t.Errorf("breaking change: %+v", got)
	}
	if fixed := h.log[1]; len(fixed.Items) != 1 || fixed.Items[0] != "fixed: "+broken.Problems[0] {
		t.Errorf("fixing change: %+v", fixed)
	}
	if !h.log[0].Show() {
		t.Error("the app deploys into the page's namespace, so its changes show")
	}
}
