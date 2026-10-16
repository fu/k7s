// Package cmd wires the k7s command line. Flags and kubeconfig handling come
// from k8s.io/cli-runtime, the same package kubectl uses, so -n, --context,
// --kubeconfig, --as, ... behave exactly like they do in kubectl.
package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"
	"golang.org/x/term"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"

	"k7s/internal/kube"
	"k7s/internal/pods"
	"k7s/internal/tui"
)

var configFlags = genericclioptions.NewConfigFlags(true)

type options struct {
	allNamespaces bool
	selector      string
	refresh       time.Duration
	once          bool
}

func newRootCmd() *cobra.Command {
	o := &options{}
	run := func(cmd *cobra.Command, args []string) error {
		view := ""
		if len(args) == 1 {
			view = args[0]
		}
		return runView(cmd.Context(), o, view)
	}
	views := strings.Join(tui.ViewNames(), ", ")
	root := &cobra.Command{
		Use:   "k7s [view]",
		Short: "k7s - live kubectl views: more than kubectl, less than k9s",
		Long: `k7s shows kubectl-style views that keep updating in place.

Views: ` + views + ` (default: pods). Inside k7s they are menus
you switch between with their first letter.

Examples:
  k7s                          # pods of the current namespace
  k7s secrets -n my-namespace
  k7s get pods -A -l app=web   # "get" works too, like kubectl
  k7s po --once                # print one pod table and exit`,
		Args:         cobra.MaximumNArgs(1),
		SilenceUsage: true,
		RunE:         run,
	}
	configFlags.AddFlags(root.PersistentFlags())
	pf := root.PersistentFlags()
	pf.BoolVarP(&o.allNamespaces, "all-namespaces", "A", false, "Show all namespaces")
	pf.StringVarP(&o.selector, "selector", "l", "", "Label selector for pods (e.g. app=web)")
	pf.DurationVar(&o.refresh, "refresh", 5*time.Second, "Metrics refresh interval")
	pf.BoolVar(&o.once, "once", false, "Print a single pod table and exit (default when stdout is not a terminal)")

	root.AddCommand(&cobra.Command{
		Use:   "get [view]",
		Short: "Same as k7s [view], for kubectl muscle memory",
		Args:  cobra.MaximumNArgs(1),
		RunE:  run,
	})
	return root
}

// Execute runs the root command.
func Execute() {
	// client-go logs through klog to stderr, which would corrupt the TUI.
	// Errors are surfaced in the UI instead.
	klog.SetLogger(logr.Discard())
	rest.SetDefaultWarningHandler(rest.NoWarnings{})

	if err := newRootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}

func runView(ctx context.Context, o *options, arg string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	o.refresh = max(o.refresh, time.Second)

	// Known views open directly; anything else shows pods with a hint.
	view, notice := "pods", ""
	if arg != "" {
		if v, ok := tui.ResolveView(arg); ok {
			view = v
		} else {
			notice = fmt.Sprintf("%q is not a k7s view (yet), showing pods. Views: %s", arg, strings.Join(tui.ViewNames(), ", "))
		}
	}

	client, err := kube.New(configFlags)
	if err != nil {
		return err
	}
	// Fail fast (auth, bad context) before switching to the TUI, and let
	// exec credential plugins prompt on a normal terminal.
	preflight, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := client.Kube.Discovery().RESTClient().Get().AbsPath("/version").Do(preflight).Error(); err != nil {
		return fmt.Errorf("connecting to %s: %w", client.Context, err)
	}

	if o.once || !term.IsTerminal(int(os.Stdout.Fd())) {
		if view != "pods" {
			return fmt.Errorf("--once only prints pods")
		}
		ns := client.Namespace
		if o.allNamespaces {
			ns = metav1.NamespaceAll
		}
		return pods.PrintSnapshot(ctx, pods.NewSource(client, ns, o.selector, o.refresh), os.Stdout, o.allNamespaces)
	}
	return tui.Run(ctx, configFlags, client, tui.Info{
		Context:       client.Context,
		Server:        client.Server,
		Namespace:     client.Namespace,
		AllNamespaces: o.allNamespaces,
		Selector:      o.selector,
		Refresh:       o.refresh,
		Start:         view,
		Notice:        notice,
	})
}
