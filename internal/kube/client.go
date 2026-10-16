// Package kube builds Kubernetes clients from kubectl-compatible flags.
package kube

import (
	"sort"

	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	metricsclient "k8s.io/metrics/pkg/client/clientset/versioned"
)

// Client bundles the typed API clients plus the resolved kubeconfig context.
type Client struct {
	Config    *rest.Config
	Kube      kubernetes.Interface
	Dynamic   dynamic.Interface // for CRDs such as Argo CD / Flux objects
	Metrics   metricsclient.Interface
	Namespace string
	Context   string
	Server    string
}

// New resolves kubeconfig the same way kubectl does.
func New(flags *genericclioptions.ConfigFlags) (*Client, error) {
	loader := flags.ToRawKubeConfigLoader()
	ns, _, err := loader.Namespace()
	if err != nil {
		return nil, err
	}
	raw, err := loader.RawConfig()
	if err != nil {
		return nil, err
	}
	ctxName := raw.CurrentContext
	if flags.Context != nil && *flags.Context != "" {
		ctxName = *flags.Context
	}

	cfg, err := flags.ToRESTConfig()
	if err != nil {
		return nil, err
	}
	cfg.QPS, cfg.Burst = 50, 100

	kc, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}
	mc, err := metricsclient.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}
	dc, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}
	return &Client{Config: cfg, Kube: kc, Dynamic: dc, Metrics: mc, Namespace: ns, Context: ctxName, Server: cfg.Host}, nil
}

// ForContext returns a client for another context of the same kubeconfig,
// keeping the user's --kubeconfig and impersonation flags.
func ForContext(base *genericclioptions.ConfigFlags, name string) (*Client, error) {
	f := genericclioptions.NewConfigFlags(true)
	f.KubeConfig = base.KubeConfig
	f.Impersonate, f.ImpersonateUID, f.ImpersonateGroup = base.Impersonate, base.ImpersonateUID, base.ImpersonateGroup
	f.Context = &name
	return New(f)
}

// ContextInfo describes one kubeconfig context.
type ContextInfo struct {
	Name, Namespace, Server string
}

// Contexts lists the kubeconfig's contexts, sorted by name.
func Contexts(flags *genericclioptions.ConfigFlags) ([]ContextInfo, error) {
	raw, err := flags.ToRawKubeConfigLoader().RawConfig()
	if err != nil {
		return nil, err
	}
	out := make([]ContextInfo, 0, len(raw.Contexts))
	for name, c := range raw.Contexts {
		ci := ContextInfo{Name: name, Namespace: c.Namespace}
		if cl := raw.Clusters[c.Cluster]; cl != nil {
			ci.Server = cl.Server
		}
		out = append(out, ci)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
