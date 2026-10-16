# k7s

One namespace, everything in it, live: more than `kubectl`, less than `k9s`.

k7s shows a namespace on one page that updates in place: the GitOps apps that
deploy it, its pods (with CPU/MEM) and secrets — and below them a change log
of everything that happens there, including what happened before you started.

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
k7s                         # the current namespace
k7s -n shop                 # namespace shop
k7s -n shop -l app=web      # only one app in it
k7s -n shop --once          # print the pod table once and exit (also when piped)
k7s --refresh 2s            # metrics poll interval (default 5s)
```

There are no sub commands: you pick a namespace (and optionally a filter),
and everything in it is shown.

## The page

```
 k7s │ ctx dev │ ns shop │ filter app=web │ pods 3 ●3 ●0 ●0 │ secrets 2 │ gitops ● 1 apps │ ● watching

gitops (1)
 #  SYNC  HEALTH  NAME  DEST  PARENT      SOURCE              REV      SYNCED
 1  ●     ●       web   shop  ● main-app  deploy/apps/web     f91dfdc  2h

pods (3)
 #  NAME                   READY  STATUS  RESTARTS  CPU  %CPU/R  %CPU/L  MEM  %MEM/R  %MEM/L  AGE
 1  web-7d4b9c6f5-x2k8q    1/1    ●       0         3m  1%      -       248Mi 48%    8%      3h
 …

secrets (2)
 #  NAME     TYPE    KEYS  AGE
 …

─ changes (42) ─────────────────────────────────────────────────────────────
13:02:11 EVENT    deployment/web  ScalingReplicaSet: Scaled up replica set web-7d4b9c6f5 to 3
13:02:15 MODIFIED web-7d4b9c6f5-x2k8q  Running 1/1  status ContainerCreating → Running
 contexts  namespaces  filter │ gitops  pods  secrets   logs  shell  forward  [q/a] changes  [esc] quit
```

**Sections** (in this order; each shows the same rows wherever it appears):

1. **gitops** — only the Argo CD / Flux apps responsible for what is listed:
   those deploying a listed secret or the workload (Deployment,
   StatefulSet, …) of a listed pod, read from Argo CD's managed resources /
   Flux's inventory; without a filter, every app deploying into the
   namespace. SYNC and HEALTH lights; PARENT is the app that deploys this
   one (app of apps), colored by the worst light up that chain.
2. **pods** — `#`, READY, a status light (green running, amber starting,
   red failing/terminating), RESTARTS ("x ago"), CPU, MEM and their % of the
   pod's request (`/R`) and limit (`/L`, `-` = none). CPU/MEM show ↑/↓ versus
   the previous poll, changed cells flash, new pods are highlighted, deleted
   ones stay struck through for 8s, port-forwarded pods show `⇄:<port>`.
3. **secrets** — name, type, number of keys, age. Enter opens one: its keys
   with the values masked; `r` reveals them. Values are only fetched for the
   opened secret, never kept in the list.

The status line has one **gitops light**: the worst of the responsible apps
and their parents, so green pods under a broken deployment chain do not
look fine. The cyan "watching" dot breathes once per metrics refresh.

**Change log** (`q`/`a` scroll it, line by line) — everything about what is
shown, in time order:

- pod adds/deletes and status, ready and restart changes; container exits are
  explained (`exit OOMKilled(137) — out of memory, hit its memory limit`)
- secrets added, deleted and updated
- sync / health / revision changes of the responsible apps (with every
  degraded or out-of-sync resource on its own line)
- all Kubernetes events of the namespace — warnings (failed probes,
  scheduling, mounts…) and normal ones (scaling, rollouts, pulls, kills)
- at start: the events Kubernetes still keeps (about the last hour), so you
  see what happened before — including pods that are already gone — and a
  `STATE` line for everything already unhealthy

With a filter, only events about listed objects (and their Deployments /
ReplicaSets) are shown.

## Keys

The navigation bar shows the menus with their key letter in cyan:

| key | |
|-----|---|
| `c` / `n` / `f` | pick the **c**ontext / **n**amespace / **f**ilter; enter switches and returns |
| `g` / `p` / `s` | show or hide the **g**itops / **p**ods / **s**ecrets section |
| `↑↓`, `0-9` | move; jump to row `#` of the current section |
| `l` / `h` / `w` | on a pod: **l**ogs, s**h**ell, for**w**ard a port (again: stop) |
| `enter` | on a pod: logs; on a secret: open it |
| `q` / `a` | scroll the change log up / down |
| `esc` | back; on the main page: quit |

The **filter** pane suggests selectors from the app labels in the namespace
(`app.kubernetes.io/instance`, `app.kubernetes.io/name`, `app`, `k8s-app`,
`release` …) with how many pods and secrets each matches, plus "everything".
The **namespace** pane shows pods running/pending/failing and CPU/MEM per
namespace.

Logs follow the last 500 lines like `kubectl logs -f --tail=500`; the shell
uses client-go's exec (like `kubectl exec -it`, `bash` else `sh`), type
`exit` to come back; port-forwarding works like `kubectl port-forward` (same
local port when free). Pods with several containers or ports get a picker.

## Code layout

```
cmd/                  CLI: k7s [-n] [-l]
internal/kube         clients, contexts, exec (shell), port-forward
internal/pods         pod rows, watch + metrics + events, logs, --once table
internal/secrets      secret watch (values dropped) and on-demand values
internal/gitops       Argo CD / Flux apps, what they manage, parents
internal/namespaces   namespace summaries
internal/resources    filter suggestions from app labels
internal/tui          the main page, its sections and sub screens
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
