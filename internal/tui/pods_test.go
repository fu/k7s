package tui

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/fu/k7s/internal/pods"
)

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
	p := podsState{rows: map[string]*rowState{}}
	first := &corev1.ContainerStateTerminated{Reason: "Error", ExitCode: 1, FinishedAt: metav1.NewTime(time.Now().Add(-time.Minute))}
	p.apply(pods.Event{Type: pods.Added, Row: pods.FromPod(crashingPod(3, first)), Initial: true})

	oom := &corev1.ContainerStateTerminated{Reason: "OOMKilled", ExitCode: 137, Message: "heap\nmore", FinishedAt: metav1.Now()}
	p.apply(pods.Event{Type: pods.Modified, Row: pods.FromPod(crashingPod(4, oom))})

	if len(p.events) != 1 {
		t.Fatalf("want 1 change, got %d", len(p.events))
	}
	got := p.events[0].detail
	for _, want := range []string{"restarts 3 → 4", "exit OOMKilled(137)", "out of memory", ": heap"} {
		if !strings.Contains(got, want) {
			t.Errorf("detail %q misses %q", got, want)
		}
	}
}

func TestUnhealthyPodsAreExplainedAtStartup(t *testing.T) {
	p := podsState{rows: map[string]*rowState{}}
	exit := &corev1.ContainerStateTerminated{Reason: "Error", ExitCode: 1, FinishedAt: metav1.Now()}
	p.apply(pods.Event{Type: pods.Added, Row: pods.FromPod(crashingPod(7, exit)), Initial: true})
	p.logInitialProblems()

	if len(p.events) != 1 || p.events[0].kind != stateKind {
		t.Fatalf("want one STATE line, got %+v", p.events)
	}
	if got := p.events[0].detail; !strings.Contains(got, "restarts 7") || !strings.Contains(got, "last exit Error(1) — application error") {
		t.Errorf("unexpected detail %q", got)
	}
}

func TestKubeEventsInChangeLog(t *testing.T) {
	m := &model{}
	m.pods.rows = map[string]*rowState{}
	m.applyKubeEvent(pods.KubeEvent{Namespace: "ns", Kind: "Pod", Name: "a", Type: "Warning", Reason: "BackOff", Message: "Back-off restarting", At: time.Now()})
	m.applyKubeEvent(pods.KubeEvent{Namespace: "ns", Kind: "Pod", Name: "a", Type: "Warning", Reason: "Unhealthy", Message: "Liveness probe failed", Count: 3, At: time.Now()})
	m.applyKubeEvent(pods.KubeEvent{Namespace: "ns", Kind: "Deployment", Name: "web", Type: "Normal", Reason: "ScalingReplicaSet", Message: "Scaled up to 2", At: time.Now().Add(-time.Hour), Initial: true})
	ev := m.pods.events
	if len(ev) != 2 {
		t.Fatalf("BackOff is skipped, the rest kept: %+v", ev)
	}
	// Kept in time order: the hour-old event (history) comes first.
	if ev[0].kind != pods.Normal || ev[0].name != "deployment/web" || ev[1].detail != "Unhealthy: Liveness probe failed (×3)" {
		t.Errorf("unexpected events: %+v", ev)
	}
}

func TestFilterKeepsOnlyEventsAboutListedObjects(t *testing.T) {
	m := &model{info: Info{Selector: "app=web"}}
	m.pods.rows = map[string]*rowState{"ns/web-7d4-x": {row: pods.Row{Namespace: "ns", Name: "web-7d4-x", WorkloadKind: "Deployment", WorkloadName: "web"}}}
	for _, e := range []pods.KubeEvent{
		{Namespace: "ns", Kind: "Pod", Name: "web-7d4-x", Reason: "Pulled"},
		{Namespace: "ns", Kind: "ReplicaSet", Name: "web-7d4", Reason: "SuccessfulCreate"},
		{Namespace: "ns", Kind: "Deployment", Name: "web", Reason: "ScalingReplicaSet"},
		{Namespace: "ns", Kind: "Deployment", Name: "db", Reason: "ScalingReplicaSet"},
		{Namespace: "ns", Kind: "Pod", Name: "db-0", Reason: "Pulled"},
	} {
		m.applyKubeEvent(e)
	}
	if len(m.pods.events) != 3 {
		t.Errorf("want the 3 events about web, got %d: %+v", len(m.pods.events), m.pods.events)
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

func TestMultiLineChangeEntries(t *testing.T) {
	lines := wrapChange("10:00:00 STATE    app  OutOfSync\n· ConfigMap a OutOfSync\n· Service b OutOfSync", 80)
	if len(lines) != 3 {
		t.Fatalf("want 3 lines, got %d: %q", len(lines), lines)
	}
	for _, l := range lines[1:] {
		if !strings.HasPrefix(l, strings.Repeat(" ", changeIndent)+"· ") {
			t.Errorf("item line not indented under the entry: %q", l)
		}
	}
}

func TestTallEntryCanBeScrolledToItsFirstLine(t *testing.T) {
	m := &model{width: 120}
	entries := []string{"STATE shop  OutOfSync/Degraded  auto-sync off", "STATE main-app  Synced/Degraded"}
	for i := 0; i < 17; i++ {
		entries[0] += "\n· Deployment d" + fmt.Sprint(i) + " OutOfSync"
	}
	back := 25 // more than possible: the pane clamps it
	out := m.changesPane(10, len(entries), &back, func(i int) string { return entries[i] }, "")
	if back != 9 { // 19 lines in total, 10 visible
		t.Fatalf("back clamped to %d, want 9", back)
	}
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if len(lines) != 11 || !strings.Contains(lines[1], "STATE shop") || !strings.Contains(lines[10], "d8 OutOfSync") {
		t.Fatalf("unexpected pane:\n%s", out)
	}
}

func TestMenuKeysAreUniqueFirstLetters(t *testing.T) {
	names := slices.Clone(sections)
	for _, sc := range scopes {
		names = append(names, sc.name)
	}
	seen := map[byte]string{}
	for _, name := range names {
		if other, dup := seen[name[0]]; dup {
			t.Errorf("menus %q and %q share key %q", other, name, name[0])
		}
		seen[name[0]] = name
	}
}

func TestSectionToggles(t *testing.T) {
	m := &model{}
	m.globalKey("s")
	if m.shown("secrets") || !m.shown("pods") {
		t.Fatal("s hides only the secrets section")
	}
	m.globalKey("s")
	if !m.shown("secrets") {
		t.Fatal("s again shows it again")
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
