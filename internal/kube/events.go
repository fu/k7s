package kube

import (
	"context"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"
)

// Kubernetes events of a namespace (like "kubectl events -w"), including the
// ones Kubernetes still keeps from before the watch started (about the last
// hour). They feed the change log.

// Event is a Kubernetes Event about an object in the namespace: a failed
// probe or OOM kill (Warning), a scaled ReplicaSet or pulled image (Normal), …
type Event struct {
	Namespace       string
	Kind, Name      string // the object it is about
	Type            string // Normal or Warning
	Reason, Message string
	Count           int32
	At              time.Time
	Initial         bool // from before the watch started
}

// WatchEvents emits the events of ns until ctx is done. It returns
// immediately and is best effort: without RBAC it stays silent.
func (c *Client) WatchEvents(ctx context.Context, ns string, emit func(Event)) error {
	factory := informers.NewSharedInformerFactoryWithOptions(c.Kube, 0, informers.WithNamespace(ns))
	inf := factory.Core().V1().Events().Informer()
	_, err := inf.AddEventHandler(cache.ResourceEventHandlerDetailedFuncs{
		AddFunc: func(obj any, initial bool) {
			if e, ok := obj.(*corev1.Event); ok {
				emit(from(e, initial))
			}
		},
		UpdateFunc: func(oldObj, obj any) {
			// Repeats of the same event bump its count.
			o, ok1 := oldObj.(*corev1.Event)
			e, ok2 := obj.(*corev1.Event)
			if ok1 && ok2 && count(e) > count(o) {
				emit(from(e, false))
			}
		},
	})
	if err != nil {
		return err
	}
	factory.Start(ctx.Done())
	return nil
}

func count(e *corev1.Event) int32 {
	if e.Series != nil {
		return e.Series.Count
	}
	return e.Count
}

func from(e *corev1.Event, initial bool) Event {
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
	msg := e.Message
	for i, r := range msg {
		if r == '\n' {
			msg = msg[:i]
			break
		}
	}
	return Event{
		Namespace: e.InvolvedObject.Namespace,
		Kind:      e.InvolvedObject.Kind,
		Name:      e.InvolvedObject.Name,
		Type:      e.Type,
		Reason:    e.Reason,
		Message:   msg,
		Count:     max(1, count(e)),
		At:        at,
		Initial:   initial,
	}
}
