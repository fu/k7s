// Package gitops reads GitOps state — Argo CD Applications and Flux
// Kustomizations / HelmReleases — into one engine-neutral App model.
package gitops

import (
	"context"
	"path"
	"slices"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/tools/cache"

	"github.com/fu/k7s/internal/kube"
)

// Engine is the GitOps tool found on a cluster.
type Engine string

const (
	None Engine = ""
	Argo Engine = "Argo CD"
	Flux Engine = "Flux"
)

// Detect finds the GitOps engine by looking for its API resources.
func Detect(c *kube.Client) Engine {
	switch {
	case hasResource(c, "argoproj.io/v1alpha1", "applications"):
		return Argo
	case firstServed(c, "kustomize.toolkit.fluxcd.io", "kustomizations", "v1", "v1beta2") != "":
		return Flux
	}
	return None
}

func hasResource(c *kube.Client, groupVersion, name string) bool {
	list, err := c.Kube.Discovery().ServerResourcesForGroupVersion(groupVersion)
	if err != nil {
		return false
	}
	for _, r := range list.APIResources {
		if r.Name == name {
			return true
		}
	}
	return false
}

// firstServed returns the first of versions the cluster serves for resource.
func firstServed(c *kube.Client, group, resource string, versions ...string) string {
	for _, v := range versions {
		if hasResource(c, group+"/"+v, resource) {
			return v
		}
	}
	return ""
}

// Light is a traffic-light color; higher is worse.
type Light int

const (
	Grey Light = iota
	Green
	Amber
	Red
)

// App is one deployed unit: an Argo CD Application, a Flux Kustomization or
// a Flux HelmRelease. Each engine fills in its own wording and lights.
type App struct {
	Kind            string // Application, Kustomization, HelmRelease
	Namespace, Name string
	Sync            string // engine wording, e.g. Synced/OutOfSync or Ready/Failed
	Health          string // e.g. Healthy, Progressing, Degraded
	SyncL, HealthL  Light
	OpPhase         string // Argo: last sync operation phase
	OpMessage       string
	SyncedAt        time.Time
	DestNS          string   // namespace it deploys into
	Source          string   // repo/path or chart
	Revision        string   // short commit or chart version
	Why             []string // short reasons, e.g. "auto-sync off"
	Problems        []string // every resource that is unhealthy or out of sync
	Manages         []Ref    // the objects it deploys (Argo status.resources, Flux inventory)
}

// Ref names one Kubernetes object.
type Ref struct {
	Kind, Namespace, Name string
}

// ManagesRef reports whether the app deploys the object.
func (a App) ManagesRef(r Ref) bool {
	for _, m := range a.Manages {
		if m == r {
			return true
		}
	}
	return false
}

// Parents maps each app to the app that deploys it ("app of apps"), by
// finding apps whose managed objects include another app.
func Parents(apps map[string]App) map[string]string {
	byRef := map[Ref]string{}
	for key, a := range apps {
		byRef[Ref{Kind: a.Kind, Namespace: a.Namespace, Name: a.Name}] = key
	}
	parents := map[string]string{}
	for key, a := range apps {
		for _, m := range a.Manages {
			if child, ok := byRef[m]; ok && child != key {
				parents[child] = key
			}
		}
	}
	return parents
}

// ChainLight is the worst light of an app's parents, up the chain (Grey
// when it has no parent).
func ChainLight(key string, apps map[string]App, parents map[string]string) Light {
	worst := Grey
	seen := map[string]bool{key: true}
	for p, ok := parents[key]; ok && !seen[p]; p, ok = parents[p] {
		seen[p] = true
		worst = max(worst, apps[p].Overall())
	}
	return worst
}

func (a App) Key() string        { return a.Kind + "/" + a.Namespace + "/" + a.Name }
func (a App) SyncLight() Light   { return a.SyncL }
func (a App) HealthLight() Light { return a.HealthL }
func (a App) Overall() Light     { return max(a.SyncL, a.HealthL) }
func (a App) Explain() string    { return strings.Join(a.Why, ", ") }
func (a App) syncFailed() bool   { return a.OpPhase == "Failed" || a.OpPhase == "Error" }

// why adds explanations, skipping empty and repeated ones (Flux often
// reports the same message on several conditions).
func (a *App) why(parts ...string) {
	for _, p := range parts {
		if p != "" && !slices.Contains(a.Why, p) {
			a.Why = append(a.Why, p)
		}
	}
}

// EventType is the kind of change of an App.
type EventType string

const (
	Added    EventType = "ADDED"
	Modified EventType = "MODIFIED"
	Deleted  EventType = "DELETED"
)

// Event is one App change from the watch stream.
type Event struct {
	Type    EventType
	App     App
	Initial bool // part of the initial list rather than a live change
}

// Handlers receive updates; they are called from background goroutines.
type Handlers struct {
	App    func(Event)
	Synced func()
	Error  func(error)
}

// mapper turns one watched custom resource into an App.
type mapper struct {
	gvr schema.GroupVersionResource
	to  func(*unstructured.Unstructured) App
}

// Watch watches the GitOps objects of engine across the whole cluster (like
// "kubectl get applications -A -w"). It returns immediately.
func Watch(ctx context.Context, c *kube.Client, engine Engine, h Handlers) error {
	var mappers []mapper
	switch engine {
	case Argo:
		mappers = append(mappers, mapper{argoApps, fromArgo})
	case Flux:
		if v := firstServed(c, "kustomize.toolkit.fluxcd.io", "kustomizations", "v1", "v1beta2"); v != "" {
			mappers = append(mappers, mapper{schema.GroupVersionResource{Group: "kustomize.toolkit.fluxcd.io", Version: v, Resource: "kustomizations"}, fromKustomization})
		}
		if v := firstServed(c, "helm.toolkit.fluxcd.io", "helmreleases", "v2", "v2beta2", "v2beta1"); v != "" {
			mappers = append(mappers, mapper{schema.GroupVersionResource{Group: "helm.toolkit.fluxcd.io", Version: v, Resource: "helmreleases"}, fromHelmRelease})
		}
	}

	factory := dynamicinformer.NewDynamicSharedInformerFactory(c.Dynamic, 0)
	var synced []cache.InformerSynced
	for _, mp := range mappers {
		to := mp.to
		inf := factory.ForResource(mp.gvr).Informer()
		if err := inf.SetWatchErrorHandler(func(_ *cache.Reflector, err error) { h.Error(err) }); err != nil {
			return err
		}
		_, err := inf.AddEventHandler(cache.ResourceEventHandlerDetailedFuncs{
			AddFunc: func(obj any, initial bool) {
				if u, ok := obj.(*unstructured.Unstructured); ok {
					h.App(Event{Type: Added, App: to(u), Initial: initial})
				}
			},
			UpdateFunc: func(_, obj any) {
				if u, ok := obj.(*unstructured.Unstructured); ok {
					h.App(Event{Type: Modified, App: to(u)})
				}
			},
			DeleteFunc: func(obj any) {
				if t, ok := obj.(cache.DeletedFinalStateUnknown); ok {
					obj = t.Obj
				}
				if u, ok := obj.(*unstructured.Unstructured); ok {
					h.App(Event{Type: Deleted, App: to(u)})
				}
			},
		})
		if err != nil {
			return err
		}
		synced = append(synced, inf.HasSynced)
	}
	factory.Start(ctx.Done())
	go func() {
		if cache.WaitForCacheSync(ctx.Done(), synced...) {
			h.Synced()
		}
	}()
	return nil
}

// shortRevision trims git SHAs to 7 characters (also inside Flux's
// "main@sha1:<sha>" form); chart versions stay as is.
func shortRevision(r string) string {
	isSHA := func(s string) bool { return len(s) == 40 && strings.Trim(s, "0123456789abcdef") == "" }
	if isSHA(r) {
		return r[:7]
	}
	if i := strings.LastIndex(r, ":"); i >= 0 && isSHA(r[i+1:]) {
		return strings.TrimSuffix(r[:i], "@sha1") + "@" + r[i+1:i+8]
	}
	if i := strings.LastIndex(r, "/"); i >= 0 && isSHA(r[i+1:]) { // older Flux: "main/<sha>"
		return r[:i] + "@" + r[i+1:i+8]
	}
	return r
}

// describeSource renders a repo/path or chart compactly.
func describeSource(repoURL, dir, chart string) string {
	repo := strings.TrimSuffix(path.Base(strings.TrimSuffix(repoURL, "/")), ".git")
	switch {
	case chart != "":
		return chart + " (helm)"
	case dir != "" && dir != "." && dir != "./":
		return repo + "/" + strings.TrimPrefix(dir, "./")
	}
	return repo
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	return s
}

func orUnknown(s string) string {
	if s == "" {
		return "Unknown"
	}
	return s
}
