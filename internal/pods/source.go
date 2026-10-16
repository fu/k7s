package pods

import (
	"context"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"

	"k7s/internal/kube"
)

type EventType string

const (
	Added    EventType = "ADDED"
	Modified EventType = "MODIFIED"
	Deleted  EventType = "DELETED"
	Warn     EventType = "WARNING"
)

// Warning is a Kubernetes Warning event about a pod: failed probes, OOM
// kills, scheduling and mount failures and the like.
type Warning struct {
	Namespace, Pod  string
	Reason, Message string
	Count           int32
	At              time.Time
	Initial         bool // part of the initial list rather than a live change
}

// Event is one pod change from the watch stream.
type Event struct {
	Type    EventType
	Row     Row
	Initial bool // part of the initial list rather than a live change
}

// Usage is the summed container usage of one pod.
type Usage struct {
	CPU int64 // millicores
	Mem int64 // bytes
}

// Metrics is one poll of metrics.k8s.io.
type Metrics struct {
	Usage      map[string]Usage            // keyed by namespace/name
	Containers map[string]map[string]Usage // namespace/name -> container -> usage
	At         time.Time
	Err        error
}

// Handlers receive updates from a Source. They are called from background
// goroutines and must not block for long.
type Handlers struct {
	Pod     func(Event)
	Metrics func(Metrics)
	Synced  func()
	Error   func(error)
	Warning func(Warning) // optional
}

// Source combines a pod informer (list+watch, like kubectl get -w) with a
// metrics poller (like kubectl top).
type Source struct {
	client   *kube.Client
	ns       string
	selector string
	refresh  time.Duration
}

func NewSource(c *kube.Client, ns, selector string, refresh time.Duration) *Source {
	return &Source{client: c, ns: ns, selector: selector, refresh: refresh}
}

// Start begins watching; it returns immediately and stops when ctx is done.
func (s *Source) Start(ctx context.Context, h Handlers) error {
	factory := informers.NewSharedInformerFactoryWithOptions(s.client.Kube, 0,
		informers.WithNamespace(s.ns),
		informers.WithTweakListOptions(func(o *metav1.ListOptions) { o.LabelSelector = s.selector }),
	)
	inf := factory.Core().V1().Pods().Informer()
	if err := inf.SetWatchErrorHandler(func(_ *cache.Reflector, err error) { h.Error(err) }); err != nil {
		return err
	}
	_, err := inf.AddEventHandler(cache.ResourceEventHandlerDetailedFuncs{
		AddFunc: func(obj any, initial bool) {
			if p, ok := obj.(*corev1.Pod); ok {
				h.Pod(Event{Type: Added, Row: FromPod(p), Initial: initial})
			}
		},
		UpdateFunc: func(_, obj any) {
			if p, ok := obj.(*corev1.Pod); ok {
				h.Pod(Event{Type: Modified, Row: FromPod(p)})
			}
		},
		DeleteFunc: func(obj any) {
			if t, ok := obj.(cache.DeletedFinalStateUnknown); ok {
				obj = t.Obj
			}
			if p, ok := obj.(*corev1.Pod); ok {
				h.Pod(Event{Type: Deleted, Row: FromPod(p)})
			}
		},
	})
	if err != nil {
		return err
	}

	if h.Warning != nil {
		if err := s.watchWarnings(ctx, h.Warning); err != nil {
			return err
		}
	}

	factory.Start(ctx.Done())
	go func() {
		if cache.WaitForCacheSync(ctx.Done(), inf.HasSynced) {
			h.Synced()
		}
	}()
	go s.pollMetrics(ctx, h.Metrics)
	return nil
}

// watchWarnings watches Warning events for pods (like "kubectl events
// --types=Warning -w"). It is best effort: without RBAC it stays silent.
func (s *Source) watchWarnings(ctx context.Context, emit func(Warning)) error {
	factory := informers.NewSharedInformerFactoryWithOptions(s.client.Kube, 0,
		informers.WithNamespace(s.ns),
		informers.WithTweakListOptions(func(o *metav1.ListOptions) {
			o.FieldSelector = "involvedObject.kind=Pod,type=Warning"
		}),
	)
	inf := factory.Core().V1().Events().Informer()
	_, err := inf.AddEventHandler(cache.ResourceEventHandlerDetailedFuncs{
		AddFunc: func(obj any, initial bool) {
			if e, ok := obj.(*corev1.Event); ok {
				emit(toWarning(e, initial))
			}
		},
		UpdateFunc: func(oldObj, obj any) {
			// Repeats of the same event bump its count.
			o, ok1 := oldObj.(*corev1.Event)
			e, ok2 := obj.(*corev1.Event)
			if ok1 && ok2 && eventCount(e) > eventCount(o) {
				emit(toWarning(e, false))
			}
		},
	})
	if err != nil {
		return err
	}
	factory.Start(ctx.Done())
	return nil
}

func eventCount(e *corev1.Event) int32 {
	if e.Series != nil {
		return e.Series.Count
	}
	return e.Count
}

func toWarning(e *corev1.Event, initial bool) Warning {
	at := e.LastTimestamp.Time
	if e.Series != nil && !e.Series.LastObservedTime.IsZero() {
		at = e.Series.LastObservedTime.Time
	}
	if at.IsZero() {
		at = e.EventTime.Time
	}
	if at.IsZero() {
		at = e.CreationTimestamp.Time
	}
	return Warning{
		Namespace: e.InvolvedObject.Namespace,
		Pod:       e.InvolvedObject.Name,
		Reason:    e.Reason,
		Message:   firstLine(e.Message),
		Count:     max(1, eventCount(e)),
		At:        at,
		Initial:   initial,
	}
}

func (s *Source) pollMetrics(ctx context.Context, emit func(Metrics)) {
	t := time.NewTicker(s.refresh)
	defer t.Stop()
	for {
		m := s.fetchMetrics(ctx)
		if ctx.Err() != nil {
			return
		}
		emit(m)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (s *Source) fetchMetrics(ctx context.Context) Metrics {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	list, err := s.client.Metrics.MetricsV1beta1().PodMetricses(s.ns).List(ctx, metav1.ListOptions{LabelSelector: s.selector})
	if err != nil {
		return Metrics{Err: err, At: time.Now()}
	}
	m := Metrics{
		Usage:      make(map[string]Usage, len(list.Items)),
		Containers: make(map[string]map[string]Usage, len(list.Items)),
		At:         time.Now(),
	}
	for _, pm := range list.Items {
		key := pm.Namespace + "/" + pm.Name
		var u Usage
		perContainer := make(map[string]Usage, len(pm.Containers))
		for _, c := range pm.Containers {
			cu := Usage{CPU: c.Usage.Cpu().MilliValue(), Mem: c.Usage.Memory().Value()}
			perContainer[c.Name] = cu
			u.CPU += cu.CPU
			u.Mem += cu.Mem
		}
		m.Usage[key] = u
		m.Containers[key] = perContainer
	}
	return m
}
