package pods

import (
	"context"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"

	"github.com/fu/k7s/internal/kube"
)

type EventType string

const (
	Added    EventType = "ADDED"
	Modified EventType = "MODIFIED"
	Deleted  EventType = "DELETED"
)

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
}

// Watch combines a pod informer (list+watch, like kubectl get -w) with a
// metrics poll every refresh (like kubectl top). It returns immediately and
// stops when ctx is done.
func Watch(ctx context.Context, c *kube.Client, ns, selector string, refresh time.Duration, h Handlers) error {
	factory := informers.NewSharedInformerFactoryWithOptions(c.Kube, 0,
		informers.WithNamespace(ns),
		informers.WithTweakListOptions(func(o *metav1.ListOptions) { o.LabelSelector = selector }),
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

	factory.Start(ctx.Done())
	go func() {
		if cache.WaitForCacheSync(ctx.Done(), inf.HasSynced) {
			h.Synced()
		}
	}()
	go pollMetrics(ctx, c, ns, selector, refresh, h.Metrics)
	return nil
}

func pollMetrics(ctx context.Context, c *kube.Client, ns, selector string, refresh time.Duration, emit func(Metrics)) {
	t := time.NewTicker(refresh)
	defer t.Stop()
	for {
		m := fetchMetrics(ctx, c, ns, selector)
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

func fetchMetrics(ctx context.Context, c *kube.Client, ns, selector string) Metrics {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	list, err := c.Metrics.MetricsV1beta1().PodMetricses(ns).List(ctx, metav1.ListOptions{LabelSelector: selector})
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
