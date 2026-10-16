package tui

import (
	"fmt"
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

func TestBackOffWarningsAreSkipped(t *testing.T) {
	p := podsState{rows: map[string]*rowState{}}
	p.applyWarning(pods.Warning{Namespace: "ns", Pod: "a", Reason: "BackOff", Message: "Back-off restarting", At: time.Now()})
	p.applyWarning(pods.Warning{Namespace: "ns", Pod: "a", Reason: "Unhealthy", Message: "Liveness probe failed", Count: 3, At: time.Now()})
	if len(p.events) != 1 || p.events[0].detail != "Unhealthy: Liveness probe failed (×3)" {
		t.Fatalf("unexpected events: %+v", p.events)
	}
}

func TestJumpToPodNumber(t *testing.T) {
	p := podsState{order: make([]string, 15)}
	for i := range p.order {
		p.order[i] = string(rune('a' + i))
	}
	p.jump("1")
	p.jump("2")
	if p.cursor != 11 {
		t.Fatalf("typing 12 should select row 12, cursor=%d", p.cursor)
	}
	p.jumpAt = time.Now().Add(-2 * jumpWindow) // pause: next digit starts a new number
	p.jump("3")
	if p.cursor != 2 {
		t.Fatalf("after a pause, 3 should select row 3, cursor=%d", p.cursor)
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
	seen := map[byte]string{}
	for _, mu := range menus {
		k := mu.name[0]
		if other, dup := seen[k]; dup {
			t.Errorf("menus %q and %q share key %q", other, mu.name, k)
		}
		seen[k] = mu.name
	}
}

func TestResolveView(t *testing.T) {
	for arg, want := range map[string]string{
		"pods": "pods", "po": "pods", "Secret": "secrets", "ns": "namespaces",
		"ctx": "contexts", "argocd": "gitops", "helmreleases": "gitops",
	} {
		if got, ok := ResolveView(arg); !ok || got != want {
			t.Errorf("ResolveView(%q) = %q, %v; want %q", arg, got, ok, want)
		}
	}
	if _, ok := ResolveView("deployments"); ok {
		t.Error("deployments is not a view yet")
	}
}

func TestHintsDropTheBrackets(t *testing.T) {
	got := hints("s[h]ell  [esc] back")
	if strings.ContainsAny(got, "[]") || !strings.Contains(got, "h") || !strings.Contains(got, "esc") {
		t.Errorf("hints rendered %q", got)
	}
}

func TestScrolledChangesStayPut(t *testing.T) {
	p := podsState{rows: map[string]*rowState{}}
	for i := 0; i < 20; i++ {
		p.logEvent(eventLine{at: time.Now()})
	}
	p.scrollChanges(5)
	p.logEvent(eventLine{at: time.Now()})
	if p.changesBack != 6 {
		t.Fatalf("a new change should keep the scrolled view in place, changesBack=%d", p.changesBack)
	}
}
