# k7s

One namespace, everything in it, live: more than `kubectl`, less than `k9s` - all linked up.

k7s shows a namespace on one page that updates in place: the GitOps apps that
deploy it, its deployments, replicasets, pods (with CPU/MEM) and secrets — and
below them a change log
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
k7s -n shop --once          # print the sections once and exit (also when piped)
k7s --refresh 2s            # metrics poll interval (default 5s)
```

There are no sub commands: you pick a namespace (and optionally a filter),
and everything in it is shown.

## The page

```
 k7s │ ctx dev │ ns shop │ filter app=web │ gitops ● 1 apps │ deployments 1 ●1 ●0 ●0 │ pods 3 ●3 ●0 ●0 │ secrets 2 │ ● watching

gitops
 #  SYNC  HEALTH  NAME  DEST  PARENT      SOURCE              REV      SYNCED
 1  ●     ●       web   shop  ● main-app  deploy/apps/web     f91dfdc  2h

deployments
 #  NAME  READY  UP-TO-DATE  AVAILABLE  STATUS  IMAGES    AGE
 1  web   3/3             3          3  ●       web:1.4.2  40d

replicasets
 #  NAME             REV  DESIRED  CURRENT  READY  STATUS  AGE
 1  web-7d4b9c6f5      4        3        3      3  ●       3h
 2  web-5f6c8d9b7      3        0        0      0  ●       2d

pods
 #  NAME                   READY  STATUS  RESTARTS  CPU  %CPU/R  %CPU/L  MEM  %MEM/R  %MEM/L  AGE
 1  web-7d4b9c6f5-x2k8q    1/1    ●       0         3m  1%      -       248Mi 48%    8%      3h
 …

secrets
 #  NAME     TYPE    KEYS  AGE
 …

─ changes (42) ─────────────────────────────────────────────────────────────
13:02:10 MODIFIED deployment/web  2/3  rollout web:1.4.1 → web:1.4.2, up-to-date 3 → 0
13:02:11 EVENT    deployment/web  ScalingReplicaSet: Scaled up replica set web-7d4b9c6f5 to 3
13:02:15 MODIFIED web-7d4b9c6f5-x2k8q  Running 1/1  status ContainerCreating → Running
 contexts  namespaces  filter │ gitops  deployments  replicasets  pods  secrets   logs  shell  forward  [q/a] changes  [esc] quit
```

**Sections** (in this order; each shows the same rows wherever it appears):

1. **gitops** — only the Argo CD / Flux apps responsible for what is listed:
   those deploying a listed secret or the workload (Deployment,
   StatefulSet, …) of a listed pod, read from Argo CD's managed resources /
   Flux's inventory; without a filter, every app deploying into the
   namespace. SYNC and HEALTH lights; PARENT is the app that deploys this
   one (app of apps), colored by the worst light up that chain.
2. **deployments** — READY, UP-TO-DATE, AVAILABLE, a status light (green
   all ready and up to date, amber rolling out or scaling, red stuck:
   progress deadline exceeded or replica failure) and the images.
3. **replicasets** — one per rollout revision (REV) with desired, current
   and ready replicas; old revisions scaled to zero are dimmed.
4. **pods** — `#`, READY, a status light (green running, amber starting,
   red failing/terminating), RESTARTS ("x ago"), CPU, MEM and their % of the
   pod's request (`/R`) and limit (`/L`, `-` = none). CPU/MEM show ↑/↓ versus
   the previous poll, changed cells flash, new pods are highlighted, deleted
   ones stay struck through for 8s, port-forwarded pods show `⇄:<port>`.
5. **claims** — the PersistentVolumeClaims: a status light (green bound,
   amber pending or resizing, red lost), the volume, capacity, access modes
   (RWO, RWX, …) and storage class. Toggle with `m` (`c` is contexts).
6. **volumes** — the PersistentVolumes bound to claims of the namespace
   (with a filter: to the listed claims): status (yellow released: the
   claim is gone, the data may remain), claim, capacity, reclaim policy.
7. **secrets** — name, type, number of keys, age. Enter opens one: its keys
   with the values masked; `r` reveals them. Values are only fetched for the
   opened secret, never kept in the list.

**Links**: the selected row lights up (dark teal) everything linked to it,
across sections, following app → Deployment → ReplicaSet → pods → secrets
and claims → volumes.
Select an app to see its deployments, their replicasets, pods and the
secrets they use. Select a pod to see its replicaset, deployment, app and
secrets. Select a secret to see who uses it, up to the app. Secrets are
used through volumes, `env`/`envFrom` and image pull secrets; jobs belong
to their CronJob / KEDA ScaledJob by name.

The status line has one **gitops light**: the worst of the responsible apps
and their parents, so green pods under a broken deployment chain do not
look fine. The cyan "watching" dot breathes once per metrics refresh.

**Change log** (`q`/`a` scroll it, line by line) — everything about what is
shown, in time order. Each line starts with the time and a tag (up to three
letters) for the kind it is about: kubectl's short name where it has a short
one (`po` pods, `rs` replicasets, `pvc` claims, `pv` volumes, `ks`/`hr`
Flux), else k7s' own (`dp` deployments, `se` secrets, `ap` Argo CD apps). Kubernetes events get the
tag of the object they are about; events about other kinds (Jobs, …) are
`ev`.

```
16:59:03 po  MODIFIED web-7d4-x  Running 2/2  status PodInitializing → Running
16:59:04 rs  ADDED    web-7d4  revision 4, 3 replicas
16:59:05 pvc MODIFIED data-db-0  Bound  resized 10Gi → 20Gi
16:59:06 ev  EVENT    job/agent-x  Completed: Job completed
```

It records:

- deployment rollouts (image changes), scaling, readiness and why a rollout
  is stuck; new replicaset revisions and their scaling
- pod adds/deletes and status, ready and restart changes; container exits are
  explained (`exit OOMKilled(137) — out of memory, hit its memory limit`)
- secrets added, deleted and updated
- sync / health / revision changes of the responsible apps, and why an
  app is not green, one line each: the resources Argo CD / Flux list, plus
  what k7s finds itself in what the app manages (Argo CD 3 no longer keeps
  resource health in the Application). k7s looks for child apps that are
  not green, Deployments, StatefulSets, DaemonSets and Jobs that are not
  available, with their failing pods and why (CrashLoopBackOff, a missing
  secret, …), and claims that are not bound.
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
| `g` `e` `r` `p` `m` `v` `s` | show or hide the **g**itops / d**e**ployments / **r**eplicasets / **p**ods / clai**m**s / **v**olumes / **s**ecrets section |
| `↑↓`, `0-9` | move; jump to row `#` of the current section |
| `d` | on any row: **d**escribe it, the same text as `kubectl describe` (from kubectl's own describe code) |
| `l` / `h` / `w` | on a pod: **l**ogs, s**h**ell, for**w**ard a port (again: stop) |
| `enter` | on a pod: logs; on a secret: open it |

The keys on the selected row are **commands**. The config decides which
commands each section offers and on which keys (see [Config](#config)). The
ones above are the defaults. More exist than are bound by default:

| command | sections | |
|---|---|---|
| `describe` | every | `kubectl describe` |
| `yaml` | every | `kubectl get -o yaml`, without managedFields; secret values masked |
| `logs` | pods | `kubectl logs -f --tail=500` of a container |
| `previous-logs` | pods | `kubectl logs -p`: the run before the last restart |
| `shell` | pods | `kubectl exec -it … sh` |
| `forward` | pods | `kubectl port-forward` (again: stop) |
| `open` | secrets | the keys, values masked until `r` |
| `q` / `a` | scroll the change log up / down |
| `Q` / `A` | move the divider up / down: a taller / shorter change log |
| `esc` | back; on the main page: quit |

The **filter** pane suggests selectors from the app labels in the namespace
(`app.kubernetes.io/instance`, `app.kubernetes.io/name`, `app`, `k8s-app`,
`release` …) with how many deployments, replicasets, pods and secrets each
matches, plus "everything" (`enter` applies). Below them are the row
**states** of each kind (pods: running, pending, failing, completed;
deployments: ready, progressing, stuck, scaled-down; replicasets: active,
old). `enter` or `space` shows ● or hides ○ a state. That's saved in the
config and applies to every namespace. By default completed pods and old
replicasets are hidden. The status line counts what is hidden
(`pods 8 … (+5 hidden)`). Hidden rows stay in the change log, and links
still pass through them. To exclude by label, use the selector syntax,
e.g. `-l '!job-name'` (no pods created by Jobs).
The **namespace** pane shows pods running/pending/failing and CPU/MEM per
namespace.

Logs follow the last 500 lines like `kubectl logs -f --tail=500`; the shell
uses client-go's exec (like `kubectl exec -it`, `bash` else `sh`), type
`exit` to come back; port-forwarding works like `kubectl port-forward` (same
local port when free). Pods with several containers or ports get a picker.

## Config

`~/.config/k7s/config.yaml` (or `$XDG_CONFIG_HOME/k7s/config.yaml`, or
`--config <file>`) keeps the colors and the state k7s comes back to: hidden
sections, hidden row states, the last namespace per context (used without
`-n`).

The defaults are [`internal/config/default.yaml`](internal/config/default.yaml),
compiled into k7s. Change a default there, not in Go. On the first start k7s
writes that file, comments and all, as your config. After that k7s rewrites
the file whenever you toggle a section or state or switch namespace, so
comments do not survive.

The `commands` part binds commands to keys, per section; `"*"` is for every
section, and several keys go in one string:

```yaml
commands:
  "*":
    describe: d
    yaml: y               # not bound by default
  pods:
    logs: l enter
    previous-logs: o      # not bound by default
    shell: h
    forward: w
  secrets:
    open: enter
```

A command a section does not list is not offered (`pods: {}` offers none).
A section missing from `commands` gets its defaults. Page keys (`c n f`, the
section keys, `q a Q A`, `j k G`, digits, `esc`) win over commands.

Keys missing from your file take their defaults. `version` is the file format:
older files are migrated, and a file from a newer k7s is used as far as
understood but never overwritten. The version is bumped whenever a key is
added, because an older k7s would otherwise drop the new key when saving.

## Code layout

```
cmd/                       CLI: k7s [-n] [-l] [--once] [--config]
internal/config/           the YAML config: colors, hidden sections and states,
  default.yaml             last namespaces; the defaults (compiled in)
internal/kube/             cluster access: clients, contexts, a generic watch,
                           exec, port-forward, events, value formatting
internal/resources/        the contract every kind implements
  resource.go              Resource (a section), Keyed, Stateful, Linker/Link, Host, Change
  row.go                   Column, Row, Cell + Tone — what to show, not how
  command.go               Command (what a row can do) and its Target
  steps.go                 what a command leads to: Picker, Viewer, Exec
  commands/                commands for every kind: describe, yaml
  gitops/ deployments/ replicasets/ pods/ pvcs/ pvs/ secrets/
                           one folder per kind, each with
                             resource.go  watch, state, change log, links
                             row.go       its columns and cells
                             commands/    what its rows can do, one file each
                                          (pods: logs, shell, forward; secrets: open)
  restest/                 a recording Host for testing kinds
internal/tui/              everything drawn, the same way for every kind
  app.go                   the registry of kinds and their commands, screens, navigation bar
  page.go                  the page: sections, selection, # jump, commands bound by the config
  links.go                 the link view: what the selected row links to
  table.go                 row layout: widths, shrinking, truncation
  picker.go  viewer.go     the generic screens of Picker / Viewer steps
  terminal.go              Exec steps: terminal hand-over (+ size queue)
  changes.go               the change log and Kubernetes events
  contexts.go namespaces.go filter.go   the scope menus (pickers)
  once.go                  --once: the same rows, printed
```

Adding a kind (deployments and replicasets were added this way) means a
folder under `internal/resources` with `resource.go` and `row.go` and one
line in the registry in `internal/tui/app.go`; its watch uses `kube.Watch`.
Adding a command means a file in the kind's `commands/` folder (listed in its
`All()`), and a key for it in `default.yaml` if it should be bound by
default. Its steps (Picker, Viewer, Exec) are drawn by the tui.

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
