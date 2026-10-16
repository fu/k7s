package kube

import (
	"context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"
)

// Handlers receive what a watch sees; they are called from background
// goroutines and must not block for long.
type Handlers[T any] struct {
	Object func(obj T, deleted, initial bool)
	Synced func() // the first list has arrived
	Error  func(error)
}

// Watch lists and watches one typed kind in ns matching selector (like
// "kubectl get -w"); informer picks it from the factory, e.g.
// f.Apps().V1().Deployments().Informer(). It returns immediately and stops
// when ctx is done.
func Watch[T any](ctx context.Context, c *Client, ns, selector string,
	informer func(informers.SharedInformerFactory) cache.SharedIndexInformer, h Handlers[T]) error {
	factory := informers.NewSharedInformerFactoryWithOptions(c.Kube, 0, informers.WithNamespace(ns),
		informers.WithTweakListOptions(func(o *metav1.ListOptions) { o.LabelSelector = selector }))
	inf := informer(factory)
	if err := inf.SetWatchErrorHandler(func(_ *cache.Reflector, err error) { h.Error(err) }); err != nil {
		return err
	}
	emit := func(obj any, deleted, initial bool) {
		if t, ok := obj.(cache.DeletedFinalStateUnknown); ok {
			obj = t.Obj
		}
		if o, ok := obj.(T); ok {
			h.Object(o, deleted, initial)
		}
	}
	_, err := inf.AddEventHandler(cache.ResourceEventHandlerDetailedFuncs{
		AddFunc:    func(obj any, initial bool) { emit(obj, false, initial) },
		UpdateFunc: func(_, obj any) { emit(obj, false, false) },
		DeleteFunc: func(obj any) { emit(obj, true, false) },
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
	return nil
}
