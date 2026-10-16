// Package secrets watches secrets for the secrets view. Values are dropped
// as they arrive; they are only fetched for one secret on request.
package secrets

import (
	"context"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"

	"github.com/fu/k7s/internal/kube"
)

// Row is one secret without its values.
type Row struct {
	Namespace, Name string
	Type            string // shortened, e.g. "tls", "helm release"
	Keys            int
	Created         time.Time
}

func (r Row) Key() string { return r.Namespace + "/" + r.Name }

// Handlers receive updates; they are called from background goroutines.
type Handlers struct {
	Row    func(r Row, deleted bool)
	Synced func()
	Error  func(error)
}

// Watch watches the secrets of ns ("" = all namespaces) matching selector.
// It returns immediately and stops when ctx is done.
func Watch(ctx context.Context, c *kube.Client, ns, selector string, h Handlers) error {
	factory := informers.NewSharedInformerFactoryWithOptions(c.Kube, 0, informers.WithNamespace(ns),
		informers.WithTweakListOptions(func(o *metav1.ListOptions) { o.LabelSelector = selector }))
	inf := factory.Core().V1().Secrets().Informer()
	// Drop the values as they arrive: the list only needs the key names.
	if err := inf.SetTransform(func(obj any) (any, error) {
		if s, ok := obj.(*corev1.Secret); ok {
			for k := range s.Data {
				s.Data[k] = nil
			}
			s.StringData = nil
		}
		return obj, nil
	}); err != nil {
		return err
	}
	if err := inf.SetWatchErrorHandler(func(_ *cache.Reflector, err error) { h.Error(err) }); err != nil {
		return err
	}
	emit := func(obj any, deleted bool) {
		if t, ok := obj.(cache.DeletedFinalStateUnknown); ok {
			obj = t.Obj
		}
		if s, ok := obj.(*corev1.Secret); ok {
			h.Row(Row{Namespace: s.Namespace, Name: s.Name, Type: ShortType(string(s.Type)),
				Keys: len(s.Data), Created: s.CreationTimestamp.Time}, deleted)
		}
	}
	_, err := inf.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    func(obj any) { emit(obj, false) },
		UpdateFunc: func(_, obj any) { emit(obj, false) },
		DeleteFunc: func(obj any) { emit(obj, true) },
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

// Values fetches one secret's data.
func Values(ctx context.Context, c *kube.Client, ns, name string) (map[string][]byte, error) {
	s, err := c.Kube.CoreV1().Secrets(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	return s.Data, nil
}

// ShortType shortens a secret type for display, e.g. "kubernetes.io/tls" → "tls".
func ShortType(t string) string {
	switch t {
	case "kubernetes.io/tls":
		return "tls"
	case "kubernetes.io/dockerconfigjson", "kubernetes.io/dockercfg":
		return "docker config"
	case "kubernetes.io/service-account-token":
		return "sa token"
	case "kubernetes.io/basic-auth":
		return "basic auth"
	case "kubernetes.io/ssh-auth":
		return "ssh auth"
	case "bootstrap.kubernetes.io/token":
		return "bootstrap token"
	}
	if strings.HasPrefix(t, "helm.sh/release") {
		return "helm release"
	}
	return t
}
