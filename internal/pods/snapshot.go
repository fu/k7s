package pods

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"text/tabwriter"
	"time"
)

// PrintSnapshot prints one kubectl-style table (with metrics) and returns.
func PrintSnapshot(ctx context.Context, src *Source, w io.Writer, allNamespaces bool) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var mu sync.Mutex
	rows := map[string]Row{}
	var metrics Metrics
	synced := make(chan struct{})
	gotMetrics := make(chan struct{}, 1)
	errc := make(chan error, 1)

	err := src.Start(ctx, Handlers{
		Pod: func(e Event) {
			mu.Lock()
			defer mu.Unlock()
			if e.Type == Deleted {
				delete(rows, e.Row.Key())
			} else {
				rows[e.Row.Key()] = e.Row
			}
		},
		Metrics: func(m Metrics) {
			mu.Lock()
			metrics = m
			mu.Unlock()
			select {
			case gotMetrics <- struct{}{}:
			default:
			}
		},
		Synced: func() { close(synced) },
		Error: func(err error) {
			select {
			case errc <- err:
			default:
			}
		},
	})
	if err != nil {
		return err
	}

	select {
	case <-synced:
	case err := <-errc:
		return err
	case <-time.After(30 * time.Second):
		return fmt.Errorf("timed out waiting for pod list")
	}
	select {
	case <-gotMetrics:
	case <-time.After(5 * time.Second):
	}

	mu.Lock()
	defer mu.Unlock()
	keys := make([]string, 0, len(rows))
	for k := range rows {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	tw := tabwriter.NewWriter(w, 0, 8, 3, ' ', 0)
	header := []string{"NAME", "READY", "STATUS", "RESTARTS", "CPU", "%CPU/R", "%CPU/L", "MEM", "%MEM/R", "%MEM/L", "AGE"}
	if allNamespaces {
		header = append([]string{"NAMESPACE"}, header...)
	}
	fmt.Fprintln(tw, strings.Join(header, "\t"))
	for _, k := range keys {
		r := rows[k]
		cpu, mem, cpuR, cpuL, memR, memL := "-", "-", "-", "-", "-", "-"
		if u, ok := metrics.Usage[k]; ok {
			cpu, mem = CPU(u.CPU), Mem(u.Mem)
			cpuR, cpuL = Pct(u.CPU, r.CPUReq), Pct(u.CPU, r.CPULim)
			memR, memL = Pct(u.Mem, r.MemReq), Pct(u.Mem, r.MemLim)
		}
		cols := []string{r.Name, r.ReadyString(), r.Status, Restarts(r), cpu, cpuR, cpuL, mem, memR, memL, Age(r.Created)}
		if allNamespaces {
			cols = append([]string{r.Namespace}, cols...)
		}
		fmt.Fprintln(tw, strings.Join(cols, "\t"))
	}
	if metrics.Err != nil {
		tw.Flush()
		fmt.Fprintf(w, "\nmetrics unavailable: %v\n", metrics.Err)
		return nil
	}
	return tw.Flush()
}
