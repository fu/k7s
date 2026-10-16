// Package cmd wires the k7s command line. Flags and kubeconfig handling come
// from k8s.io/cli-runtime, the same package kubectl uses, so -n, --context,
// --kubeconfig, --as, ... behave exactly like they do in kubectl.
package cmd

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"
	"golang.org/x/term"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"

	"github.com/fu/k7s/internal/kube"
	"github.com/fu/k7s/internal/tui"
)

var configFlags = genericclioptions.NewConfigFlags(true)

// version is set at release build time (-ldflags "-X …/cmd.version=…").
var version = "dev"

type options struct {
	selector string
	refresh  time.Duration
	once     bool
}

func newRootCmd() *cobra.Command {
	o := &options{}
	root := &cobra.Command{
		Use:   "k7s [-n namespace] [-l selector]",
		Short: "k7s - one namespace, everything in it, live",
		Long: `k7s shows one namespace on one page, updating in place: the gitops apps
that deploy it, its pods (with CPU/MEM) and secrets, and a change log of
everything that happens there — including the events from before you started.

Pick the namespace with -n (default: the context's namespace) and narrow it
to one app with -l. Inside k7s, c / n / f switch context, namespace and
filter; p / s / g show or hide the pods, secrets and gitops sections.

Examples:
  k7s                       # the current namespace
  k7s -n shop -l app=web    # one app in namespace shop
  k7s -n shop --once        # print the sections once and exit`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		Version:      version,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return run(cmd.Context(), o)
		},
	}
	configFlags.AddFlags(root.Flags())
	root.Flags().StringVarP(&o.selector, "selector", "l", "", "Label selector to narrow everything to one app (e.g. app=web)")
	root.Flags().DurationVar(&o.refresh, "refresh", 5*time.Second, "Metrics refresh interval")
	root.Flags().BoolVar(&o.once, "once", false, "Print the sections once and exit (default when stdout is not a terminal)")
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

func run(ctx context.Context, o *options) error {
	if ctx == nil {
		ctx = context.Background()
	}
	o.refresh = max(o.refresh, time.Second)

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

	info := tui.Info{
		Context:   client.Context,
		Server:    client.Server,
		Namespace: client.Namespace,
		Selector:  o.selector,
		Refresh:   o.refresh,
	}
	if o.once || !term.IsTerminal(int(os.Stdout.Fd())) {
		return tui.Print(ctx, client, info, os.Stdout)
	}
	return tui.Run(ctx, configFlags, client, info)
}
