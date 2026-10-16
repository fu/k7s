# k7s

Live kubectl views: more than `kubectl`, less than `k9s`.

`k7s get pods` is `kubectl get pods -w` that updates **in place** instead of
scrolling, with `kubectl top` metrics merged into each row and a change log
underneath that shows only what actually changed.

Built on the same libraries as kubectl (`client-go`, `cli-runtime`, `cobra`),
so `-n`, `--context`, `--kubeconfig`, `--as`, exec auth plugins (kubelogin) …
behave exactly like in kubectl. TUI: Bubble Tea + Lip Gloss.

## Install

```bash
brew install fu/tap/k7s                  # macOS / Linux
go install github.com/fu/k7s@latest      # or with Go
```

Or download a binary from the [releases](https://github.com/fu/k7s/releases).

## Usage

```bash
k7s                                # pods of the current namespace
k7s all -n shop                    # everything in one namespace on one page (overview)
k7s all -n shop -l app=web         # same, for one app
k7s secrets -n my-namespace        # start in another view: contexts, namespaces, filter, overview, pods, secrets, gitops
k7s get pods -A -l app=web         # "get" works too; kubectl short names (po, ns, ctx …) as well
k7s po -n my-namespace --once      # one pod table, kubectl-style (also used when piped)
k7s --refresh 2s                   # metrics poll interval (default 5s)
```

An unknown view (e.g. `k7s deployments`) opens pods with a notice listing the
views that exist.

Columns: #, NAME, READY, STATUS (light), RESTARTS (with "x ago"), CPU, %CPU/R,
%CPU/L, MEM, %MEM/R, %MEM/L, AGE. `/R` is usage as % of the pod's request,
`/L` as % of its limit (`-` = none set). On narrow terminals names are
shortened in the middle first, then the % columns go.

Live view (pods are listed by name):

- the cyan "watching" dot breathes once per metrics refresh and is brightest
  the moment new CPU/MEM numbers arrive
- pods are numbered (`#`) so you can jump to one and refer to it in a discussion
- status lights: green running, amber starting/pending, red terminating/failing;
  the change log says what the state is and why (pods already unhealthy at
  start get a `STATE` line with status, restarts and last exit)
- changed cells flash for 3s; CPU/MEM show ↑/↓ versus the previous poll
- the change log explains container exits when they happen
  (`exit OOMKilled(137) — out of memory, hit its memory limit`) and shows
  Kubernetes Warning events for pods: failed probes, scheduling, mounts…
- new pods are highlighted, deleted pods stay struck-through for 8s
- bottom pane: `kubectl -w` style log, e.g.
  `MODIFIED web-7d4b9c6f5-x2k8q  status CrashLoopBackOff → Running, restarts 142 → 143`

### Navigation

The bottom line is the navigation bar:

```
 contexts  namespaces  filter  overview  pods  secrets  gitops   logs  shell  forward  [q/a] changes  all Namespaces  [esc] quit
```

On the left are the **menus**, always available as global keys — the first
letter of each (shown in cyan) is its key: `c`ontexts, `n`amespaces,
`f`ilter, `o`verview, `p`ods, `s`ecrets, `g`itops. The first three set the
scope, the others are views. The active menu is highlighted. To the right are
the current screen's commands, again with the key letter in cyan (`l`ogs,
s`h`ell, for`w`ard, `r`eveal …; keys that are not letters are shown in front).

- `c` / `n` / `f`: pick a context / namespace / filter; enter switches right
  away and returns to the view you were on. k7s starts in the current kubectl
  context, the namespace from `-n` (else the context's default) and the
  filter from `-l`.
- The **filter** is a label selector for all views. Its pane suggests the
  selectors found in the namespace (from `app.kubernetes.io/instance`,
  `app.kubernetes.io/name`, `app`, `k8s-app`, `release` …) with how many pods
  and secrets each matches, plus "everything (no filter)".
- `N`: toggle between the selected namespace and all namespaces (not in the
  overview, which always shows one namespace).
- `↑↓` move, `0-9` jump to a row `#`, `q`/`a` scroll the change log.
- `esc` goes back; on the pods screen it quits.

**Overview (`o`, or `k7s all`)** stacks the views of one namespace on one
page: the pods table, then the secrets table — exactly the rows of their own
screens — with the pods change log underneath. The filter (`f`) narrows it to
one app. Pod rows support logs, shell and forward; enter on a secret opens
it. It never spans all namespaces. (`k7s get all` is refused on purpose:
kubectl's `get all` means something else.)

**Secrets (`s`)** lists the secrets live: name, type, number of keys, age.
Enter opens one and shows its keys with the values masked; `r` reveals them
(and hides them again). Values are only fetched for the opened secret and are
never kept in the list.

**GitOps (`g`)** sits next to pods. It detects the engine and lists every
Argo CD Application of the cluster with two lights — SYNC (green Synced, amber
OutOfSync, red failed sync) and HEALTH (green Healthy, amber Progressing,
red Degraded/Missing) — plus target namespace, source, revision and last
sync. Apps deploying into your namespace are highlighted. The change log
explains apps that are not green (auto-sync off, failed sync message, which
resources are degraded/out of sync) and shows sync/health/revision changes
live.

On Flux clusters the same view lists Kustomizations and HelmReleases (with a
KIND column): SYNC is the `Ready` condition (green ready, red failed, amber
reconciling/suspended), HEALTH is the `Healthy` condition for Kustomizations
and the release/test result for HelmReleases. Failures are explained with
Flux's own reason and message.

**Port-forwarding (`w` on a pod)** forwards a declared container port to
localhost — the same port number when it is free, else any free port — like
`kubectl port-forward`. Pods with several ports get a picker. Forwarded pods
show `⇄:<local port>` next to their name and the change log records start and
stop; `w` again stops it. All forwards end when k7s quits.

`l` and `h` go straight to the container when a pod has only one; otherwise a
container picker (type, state, restarts, CPU/MEM, image) comes first. Logs
follow the last 500 lines like `kubectl logs -f --tail=500`. The shell uses
client-go's exec (same as `kubectl exec -it`); type `exit` to come back. The
namespace picker shows pods running/pending/failing plus CPU/MEM per namespace
and refreshes while open.

## Code layout

```
cmd/                  CLI: k7s [view], k7s all, k7s get [view]
internal/kube         clients, contexts, exec (shell), port-forward
internal/pods         pod rows, watch + metrics, logs, --once table
internal/namespaces   namespace summaries
internal/secrets      secret watch (values dropped) and on-demand values
internal/gitops       Argo CD / Flux apps
internal/resources    filter suggestions from app labels
internal/tui          screens and navigation only
```

## Build

```bash
go build -o bin/k7s .
```

Behind a TLS-inspecting corporate proxy, Go must trust the proxy's root CA to
download modules. Note that on macOS Go uses *only* `SSL_CERT_FILE` when it is
set (instead of the Keychain), so a bundle without the corporate root fails
with `x509: certificate signed by unknown authority`. Either unset it, or
point it at a bundle that includes the corporate root:

```bash
cat /etc/ssl/cert.pem corporate-root.pem > ~/.certs/ca-bundle.pem
SSL_CERT_FILE=~/.certs/ca-bundle.pem go build -o bin/k7s .
```

## Release

Push a version tag; the `release` workflow tests, builds macOS/Linux/Windows
binaries with [GoReleaser](https://goreleaser.com), publishes a GitHub release
and updates the Homebrew cask in `fu/homebrew-tap`:

```bash
git tag v0.1.0 && git push origin v0.1.0
```

The Homebrew step needs the repository secret `HOMEBREW_TAP_GITHUB_TOKEN` (a
token allowed to push to `fu/homebrew-tap`); without it, it is skipped. Check
the config locally with
`go run github.com/goreleaser/goreleaser/v2@latest release --snapshot --clean`.

## License

[Apache License 2.0](LICENSE), the same as kubectl and client-go.
