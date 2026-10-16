// Package pods turns the pod watch stream and metrics API into display rows.
package pods

import (
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
)

// Row is the flattened, display-oriented view of a pod.
type Row struct {
	Namespace   string
	Name        string
	Ready       int
	Total       int
	Status      string
	Restarts    int32
	LastRestart time.Time
	IP          string
	Node        string
	Created     time.Time
	Containers  []Container // init and sidecar containers first, in spec order
	Ports       []Port      // declared TCP container ports, for port-forwarding

	// Most recent non-clean container exit, and which container it was.
	LastExit          Termination
	LastExitContainer string

	// Summed over all long-running containers. A limit of 0 means at least
	// one container is unbounded, so a percentage of limit is meaningless.
	CPUReq, CPULim int64 // millicores
	MemReq, MemLim int64 // bytes
}

func (r Row) Key() string         { return r.Namespace + "/" + r.Name }
func (r Row) ReadyString() string { return fmt.Sprintf("%d/%d", r.Ready, r.Total) }

// FromPod builds a Row, mirroring kubectl's pod printer for READY/STATUS/RESTARTS.
func FromPod(pod *corev1.Pod) Row {
	r := Row{
		Namespace: pod.Namespace,
		Name:      pod.Name,
		Status:    podStatus(pod),
		IP:        pod.Status.PodIP,
		Node:      pod.Spec.NodeName,
		Created:   pod.CreationTimestamp.Time,
	}

	sidecars := map[string]bool{}
	var res resources
	for i := range pod.Spec.InitContainers {
		c := &pod.Spec.InitContainers[i]
		if isSidecar(c) {
			sidecars[c.Name] = true
			res.add(c)
		}
	}
	for i := range pod.Spec.Containers {
		res.add(&pod.Spec.Containers[i])
	}
	r.CPUReq, r.MemReq = res.cpuReq, res.memReq
	if !res.cpuUnbounded {
		r.CPULim = res.cpuLim
	}
	if !res.memUnbounded {
		r.MemLim = res.memLim
	}

	r.Total = len(pod.Spec.Containers) + len(sidecars)
	for _, cs := range pod.Status.InitContainerStatuses {
		r.trackRestarts(cs)
		if sidecars[cs.Name] && cs.Started != nil && *cs.Started && cs.Ready {
			r.Ready++
		}
	}
	for _, cs := range pod.Status.ContainerStatuses {
		r.trackRestarts(cs)
		if cs.Ready {
			r.Ready++
		}
	}

	statuses := map[string]corev1.ContainerStatus{}
	for _, cs := range pod.Status.InitContainerStatuses {
		statuses[cs.Name] = cs
	}
	for _, cs := range pod.Status.ContainerStatuses {
		statuses[cs.Name] = cs
	}
	for i := range pod.Spec.InitContainers {
		c := &pod.Spec.InitContainers[i]
		kind := KindInit
		if sidecars[c.Name] {
			kind = KindSidecar
		}
		r.Containers = append(r.Containers, newContainer(c, kind, statuses))
	}
	for i := range pod.Spec.Containers {
		c := &pod.Spec.Containers[i]
		r.Containers = append(r.Containers, newContainer(c, KindMain, statuses))
		for _, p := range c.Ports {
			if p.Protocol == "" || p.Protocol == corev1.ProtocolTCP {
				r.Ports = append(r.Ports, Port{Container: c.Name, Name: p.Name, Number: int(p.ContainerPort)})
			}
		}
	}
	for _, c := range r.Containers {
		if c.Last.Failed() && (r.LastExit.IsZero() || c.Last.At.After(r.LastExit.At)) {
			r.LastExit, r.LastExitContainer = c.Last, c.Name
		}
	}
	return r
}

// Termination describes how a container last stopped.
type Termination struct {
	Reason   string
	ExitCode int32
	Message  string // the container's termination message, first line
	At       time.Time
}

func newTermination(t *corev1.ContainerStateTerminated) Termination {
	return Termination{Reason: t.Reason, ExitCode: t.ExitCode, Message: firstLine(t.Message), At: t.FinishedAt.Time}
}

func (t Termination) IsZero() bool { return t.At.IsZero() && t.Reason == "" }

// Failed reports whether the exit is worth flagging (not a clean completion).
func (t Termination) Failed() bool {
	return t.ExitCode != 0 || (t.Reason != "" && t.Reason != "Completed")
}

// Short is the compact table form, e.g. "OOMKilled(137)".
func (t Termination) Short() string {
	if t.IsZero() {
		return ""
	}
	reason := t.Reason
	if reason == "" {
		reason = "Exit"
	}
	return fmt.Sprintf("%s(%d)", reason, t.ExitCode)
}

// Describe explains the exit, e.g. "Error(1) — application error: <message>".
func (t Termination) Describe() string {
	s := t.Short()
	if h := t.Hint(); h != "" {
		s += " — " + h
	}
	if t.Message != "" {
		s += ": " + t.Message
	}
	return s
}

// Hint translates the reason / exit code into plain words.
func (t Termination) Hint() string {
	if t.Reason == "OOMKilled" {
		return "out of memory, hit its memory limit"
	}
	switch t.ExitCode {
	case 0:
		return ""
	case 1:
		return "application error"
	case 2:
		return "bad arguments / shell misuse"
	case 126:
		return "command not executable"
	case 127:
		return "command not found"
	case 134:
		return "SIGABRT, aborted"
	case 137:
		return "SIGKILL, killed (OOM or failed liveness probe)"
	case 139:
		return "SIGSEGV, segmentation fault"
	case 143:
		return "SIGTERM, asked to stop"
	}
	if t.ExitCode > 128 {
		return fmt.Sprintf("killed by signal %d", t.ExitCode-128)
	}
	return ""
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	return s
}

// Port is a TCP port a container declares.
type Port struct {
	Container, Name string
	Number          int
}

type ContainerKind string

const (
	KindMain    ContainerKind = ""
	KindInit    ContainerKind = "init"
	KindSidecar ContainerKind = "sidecar"
)

// Container is the display view of one container in a pod.
type Container struct {
	Name     string
	Image    string
	Kind     ContainerKind
	Ready    bool
	State    string // Running, a waiting/terminated reason, or Waiting
	Message  string // why it is waiting, e.g. `secret "x" not found`
	Restarts int32
	Last     Termination // current or previous termination
}

func newContainer(c *corev1.Container, kind ContainerKind, statuses map[string]corev1.ContainerStatus) Container {
	ci := Container{Name: c.Name, Image: c.Image, Kind: kind, State: "Waiting"}
	if cs, ok := statuses[c.Name]; ok {
		ci.Ready, ci.Restarts = cs.Ready, cs.RestartCount
		switch s := cs.State; {
		case s.Running != nil:
			ci.State = "Running"
		case s.Waiting != nil && s.Waiting.Reason != "":
			ci.State = s.Waiting.Reason
		case s.Terminated != nil && s.Terminated.Reason != "":
			ci.State = s.Terminated.Reason
		case s.Terminated != nil:
			ci.State = fmt.Sprintf("ExitCode:%d", s.Terminated.ExitCode)
		}
		if t := cs.State.Terminated; t != nil {
			ci.Last = newTermination(t)
		} else if t := cs.LastTerminationState.Terminated; t != nil {
			ci.Last = newTermination(t)
		}
		if w := cs.State.Waiting; w != nil {
			ci.Message = firstLine(w.Message)
		}
	}
	return ci
}

// WaitMessage is the first useful "why is it waiting" message, e.g. a
// missing secret or an image pull error. The CrashLoopBackOff message only
// says it is backing off, so it is skipped.
func (r Row) WaitMessage() string {
	for _, c := range r.Containers {
		if c.Message != "" && c.State != "CrashLoopBackOff" {
			return c.Message
		}
	}
	return ""
}

// Health buckets a pod for the colored counters and status colors.
type Health int

const (
	Healthy Health = iota
	Progressing
	Failing
	Finished
)

func (r Row) Health() Health {
	switch r.Status {
	case "Running":
		if r.Ready == r.Total {
			return Healthy
		}
		return Progressing
	case "Completed", "Succeeded":
		return Finished
	case "Pending", "ContainerCreating", "PodInitializing", "Terminating", "NotReady":
		return Progressing
	}
	if strings.HasPrefix(r.Status, "Init:") && !strings.Contains(r.Status, "Error") && !strings.Contains(r.Status, "BackOff") {
		return Progressing
	}
	return Failing
}

func (r *Row) trackRestarts(cs corev1.ContainerStatus) {
	r.Restarts += cs.RestartCount
	if t := cs.LastTerminationState.Terminated; t != nil && t.FinishedAt.After(r.LastRestart) {
		r.LastRestart = t.FinishedAt.Time
	}
}

type resources struct {
	cpuReq, cpuLim, memReq, memLim int64
	cpuUnbounded, memUnbounded     bool
}

func (r *resources) add(c *corev1.Container) {
	r.cpuReq += c.Resources.Requests.Cpu().MilliValue()
	r.memReq += c.Resources.Requests.Memory().Value()
	if l := c.Resources.Limits.Cpu().MilliValue(); l > 0 {
		r.cpuLim += l
	} else {
		r.cpuUnbounded = true
	}
	if l := c.Resources.Limits.Memory().Value(); l > 0 {
		r.memLim += l
	} else {
		r.memUnbounded = true
	}
}

func isSidecar(c *corev1.Container) bool {
	return c.RestartPolicy != nil && *c.RestartPolicy == corev1.ContainerRestartPolicyAlways
}

// podStatus is a port of the STATUS column logic in kubectl's printPod.
func podStatus(pod *corev1.Pod) string {
	reason := string(pod.Status.Phase)
	if pod.Status.Reason != "" {
		reason = pod.Status.Reason
	}

	sidecars := map[string]bool{}
	for i := range pod.Spec.InitContainers {
		if isSidecar(&pod.Spec.InitContainers[i]) {
			sidecars[pod.Spec.InitContainers[i].Name] = true
		}
	}

	initializing := false
	for i, c := range pod.Status.InitContainerStatuses {
		if t := c.State.Terminated; t != nil && t.ExitCode == 0 {
			continue
		}
		if sidecars[c.Name] && c.Started != nil && *c.Started {
			continue
		}
		initializing = true
		switch {
		case c.State.Terminated != nil:
			t := c.State.Terminated
			switch {
			case t.Reason != "":
				reason = "Init:" + t.Reason
			case t.Signal != 0:
				reason = fmt.Sprintf("Init:Signal:%d", t.Signal)
			default:
				reason = fmt.Sprintf("Init:ExitCode:%d", t.ExitCode)
			}
		case c.State.Waiting != nil && c.State.Waiting.Reason != "" && c.State.Waiting.Reason != "PodInitializing":
			reason = "Init:" + c.State.Waiting.Reason
		default:
			reason = fmt.Sprintf("Init:%d/%d", i, len(pod.Spec.InitContainers))
		}
		break
	}

	if !initializing {
		hasRunning := false
		for i := len(pod.Status.ContainerStatuses) - 1; i >= 0; i-- {
			c := pod.Status.ContainerStatuses[i]
			switch {
			case c.State.Waiting != nil && c.State.Waiting.Reason != "":
				reason = c.State.Waiting.Reason
			case c.State.Terminated != nil && c.State.Terminated.Reason != "":
				reason = c.State.Terminated.Reason
			case c.State.Terminated != nil && c.State.Terminated.Signal != 0:
				reason = fmt.Sprintf("Signal:%d", c.State.Terminated.Signal)
			case c.State.Terminated != nil:
				reason = fmt.Sprintf("ExitCode:%d", c.State.Terminated.ExitCode)
			case c.Ready && c.State.Running != nil:
				hasRunning = true
			}
		}
		if reason == "Completed" && hasRunning {
			reason = "NotReady"
			for _, cond := range pod.Status.Conditions {
				if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
					reason = "Running"
				}
			}
		}
	}

	if pod.DeletionTimestamp != nil {
		if pod.Status.Reason == "NodeLost" {
			return "Unknown"
		}
		return "Terminating"
	}
	return reason
}
