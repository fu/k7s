package pods

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/fu/k7s/internal/kube"
	"github.com/fu/k7s/internal/resources"
)

// testHost records what a kind logs.
type testHost struct{ log []resources.Change }

func (h *testHost) Context() context.Context { return context.Background() }
func (h *testHost) Client() *kube.Client     { return nil }
func (h *testHost) Namespace() string        { return "ns" }
func (h *testHost) Selector() string         { return "" }
func (h *testHost) Refresh() time.Duration   { return 5 * time.Second }
func (h *testHost) Send(tea.Msg)             {}
func (h *testHost) Log(c resources.Change)   { h.log = append(h.log, c) }
func (h *testHost) Notice(string)            {}
func (h *testHost) Listed() []resources.Ref  { return nil }
func (h *testHost) Pulse(time.Time)          {}

func newTestResource() (*Resource, *testHost) {
	h := &testHost{}
	r := New()
	r.host, r.rows = h, map[string]*rowState{}
	return r, h
}

func crashingPod(restarts int32, lastExit *corev1.ContainerStateTerminated) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "web-1"},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "app"}}},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{{
				Name:                 "app",
				RestartCount:         restarts,
				State:                corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}},
				LastTerminationState: corev1.ContainerState{Terminated: lastExit},
			}},
		},
	}
}

func TestRestartIsExplainedInChangeLog(t *testing.T) {
	r, h := newTestResource()
	first := &corev1.ContainerStateTerminated{Reason: "Error", ExitCode: 1, FinishedAt: metav1.NewTime(time.Now().Add(-time.Minute))}
	r.apply(Event{Type: Added, Row: FromPod(crashingPod(3, first)), Initial: true})

	oom := &corev1.ContainerStateTerminated{Reason: "OOMKilled", ExitCode: 137, Message: "heap\nmore", FinishedAt: metav1.Now()}
	r.apply(Event{Type: Modified, Row: FromPod(crashingPod(4, oom))})

	if len(h.log) != 1 || h.log[0].Kind != resources.Modified {
		t.Fatalf("want 1 MODIFIED change, got %+v", h.log)
	}
	for _, want := range []string{"restarts 3 → 4", "exit OOMKilled(137)", "out of memory", ": heap"} {
		if !strings.Contains(h.log[0].Detail, want) {
			t.Errorf("detail %q misses %q", h.log[0].Detail, want)
		}
	}
	// The changed cell flashes.
	row := r.Rows()[0]
	for i, c := range columns {
		if c.Title == "RESTARTS" && row.Cells[i].Tone != resources.Flash {
			t.Errorf("RESTARTS cell should flash, tone %v", row.Cells[i].Tone)
		}
	}
}

func TestUnhealthyPodsAreExplainedAtStartup(t *testing.T) {
	r, h := newTestResource()
	exit := &corev1.ContainerStateTerminated{Reason: "Error", ExitCode: 1, FinishedAt: metav1.Now()}
	r.apply(Event{Type: Added, Row: FromPod(crashingPod(7, exit)), Initial: true})
	r.logInitialProblems()

	if len(h.log) != 1 || h.log[0].Kind != resources.State {
		t.Fatalf("want one STATE line, got %+v", h.log)
	}
	if got := h.log[0].Detail; !strings.Contains(got, "restarts 7") || !strings.Contains(got, "last exit Error(1) — application error") {
		t.Errorf("unexpected detail %q", got)
	}
}

func TestRowHasACellPerColumn(t *testing.T) {
	r, _ := newTestResource()
	r.apply(Event{Type: Added, Initial: true, Row: Row{Namespace: "ns", Name: "a", Status: "Running", Ready: 1, Total: 1}})
	row := r.Rows()[0]
	if len(row.Cells) != len(columns) || row.Cells[0].Text != "a" || row.Cells[2].Tone != resources.Good {
		t.Errorf("unexpected row %+v", row)
	}
}

func TestListedIncludesWorkloads(t *testing.T) {
	r, _ := newTestResource()
	r.apply(Event{Type: Added, Initial: true, Row: Row{Namespace: "ns", Name: "web-7d4-x", WorkloadKind: "Deployment", WorkloadName: "web"}})
	got := r.Listed()
	want := []resources.Ref{{Kind: "Pod", Namespace: "ns", Name: "web-7d4-x"}, {Kind: "Deployment", Namespace: "ns", Name: "web"}}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("Listed() = %+v, want %+v", got, want)
	}
}

func TestLogsOfAPodWithSeveralContainersAsksWhich(t *testing.T) {
	r, _ := newTestResource()
	pod := Row{Namespace: "ns", Name: "a", Containers: []Container{{Name: "init", Kind: KindInit}, {Name: "app"}, {Name: "proxy", Kind: KindSidecar}}}
	p, ok := r.containers(pod, false).(*resources.Picker)
	if !ok || len(p.Rows) != 3 || p.Current != "app" {
		t.Fatalf("logs: want a picker over 3 containers starting on app, got %+v", p)
	}
	p, ok = r.containers(pod, true).(*resources.Picker)
	if !ok || len(p.Rows) != 2 {
		t.Fatalf("shell: init containers are not offered, got %+v", p)
	}
	single := Row{Namespace: "ns", Name: "b", Containers: []Container{{Name: "app"}}}
	if _, ok := r.containers(single, false).(*resources.Viewer); !ok {
		t.Error("a single container goes straight to its logs")
	}
}
