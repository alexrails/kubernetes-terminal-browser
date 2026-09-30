# CLAUDE.md

Guidance for Claude Code when working in this repository.

## What this is

`ktb` is a single-binary terminal UI (Bubble Tea) for browsing Kubernetes and
opening a shell or Rails console inside a pod. It shells out to `kubectl` and
`gcloud`; there is no server, database, or in-cluster component.

Navigation is either a one-step **bookmark** (`ktb admin-shop-dev` → Rails
console in that app's live pod) or a walk: cluster → namespace → app → pod →
action. `gcloud container clusters get-credentials` runs only when the cluster's
context is not already in kubeconfig.

The app never edits or deletes Kubernetes resources itself. Selecting a GKE
cluster does mutate the local kubeconfig via `gcloud get-credentials`, and
commands the user explicitly runs inside a container can change container state.

The user-facing README (`Readme.md`) is the most
complete description of behavior — read it before changing UX, keybindings, or
config schema, and update it when those change. User-visible changes also get a
line under `Unreleased` in `CHANGELOG.md` (Keep a Changelog; releases are
`vX.Y.Z` tags, embedded via `-ldflags -X main.version`).

## Commands

All Go commands go through `./scripts/go`, which pins `GOCACHE`/`GOMODCACHE`/
`GOPATH` under `.cache/` and sets `GOTOOLCHAIN=local`. Use it (or `make`) rather
than bare `go`, so builds do not touch the user's global module cache.

```sh
make build   # ./bin/ktb for the current OS
make install # build + install into $PREFIX/bin (default ~/.local/bin); rm first, see Makefile
make test    # unit tests
make race    # tests under the race detector
make vet     # go vet
make check   # test + race + vet
make cross   # linux/darwin × amd64/arm64 into dist/
```

Run a single package or test:

```sh
./scripts/go test ./internal/ui
./scripts/go test -run TestName ./internal/kube/kubectl
```

PTY smoke tests (require `make build` first; Python 3 stdlib only):

```sh
python3 scripts/pty_smoke.py       # fake gcloud/kubectl: cluster select, namespace, exec, Ctrl+C, resize, logs, SIGTERM
python3 scripts/auth_pty_smoke.py  # fake kubectl: Always manual mode, IfAvailable probe → ManualOnly
python3 scripts/pane_pty_smoke.py  # fake multiplexer: a pane entry of the menu, the pane's exec, a replaced pod refused
```

Integration tests against a real kind cluster are opt-in and documented in
`docs/integration.md` (build tag `integration`, env `KTB_INTEGRATION_KUBECONFIG`
and `KTB_INTEGRATION_KUBECTL`).

## Architecture

Dependencies point inward toward `domain`; `ui` is the only package that knows
about all the others.

- `cmd/ktb` — flag parsing, config load, TTY check, kubectl version probe, Bubble Tea program, SIGTERM handling.
- `internal/domain` — `Scope` (kubeconfig/context/namespace/generation), `PodRef`, `ContainerRef`, `Container`, and the typed `Error`/`ErrorKind` taxonomy (`Credentials`, `Forbidden`, `NotFound`, `Connection`, `Pod replaced`, …). No I/O.
- `internal/kube` — the `Reader` interface (`Pods`, `Namespaces`, `Pod`, `Describe`, `Events`) and `LogsOptions`. The seam that would let reads move off `kubectl` to client-go.
- `internal/kube/kubectl` — the only `Reader` implementation. Builds argv (`Flags`, `ReadArgs`, `ExecArgs`), parses JSON, classifies stderr into `domain.ErrorKind`.
- `internal/config` — `config.yaml` (refresh, timeout, actions) in `config.go`; `clusters.yaml` (clusters + bookmark targets) in `clusters.go`. Unknown YAML fields are errors. Validation lives here, not in `ui`.
- `internal/gcloud` — `get-credentials` for a cluster, run in the foreground with the TTY released; and a background `--quiet` cluster search (`ctrl+f`) over known projects or a typed project-ID prefix — never every project, since accounts can see thousands.
- `internal/auth` — per-context access `Mode` state machine: `BackgroundReady` → `NeedsAuth`/`ForegroundRetry` → `ManualOnly`. `ManualOnly` is deliberately sticky; only an explicit re-enable (`ctrl+b`) leaves it.
- `internal/process` — background child processes: process groups, bounded output buffers, cancellation, `Shutdown`.
- `internal/terminal` — foreground commands that run **only while Bubble Tea has released the TTY** (exec, gcloud, manual-mode logs). Deliberately a different lifecycle from `process`: interactive children stay in the terminal's foreground process group, so shutdown kills only that child's tree.
- `internal/pane` — the action menu's "in a new pane" entries: detects the multiplexer (tmux, zellij, herdr, WezTerm, kitty, or `pane` from `config.yaml`) and asks it to run `ktb --pane-session <Session>` in a new pane. The session is one exec plus the opener's `KUBECONFIG`/`PATH` and working directory, base64-encoded; herdr types its command into a shell, so there the session travels in the pane's environment and never reaches shell history. The pane is owned by the multiplexer, not by `ktb`: it is a third lifecycle, deliberately outside `process` tracking and `terminal`'s child tree, and it survives `ktb` exiting.
- `internal/presentation` — pure formatting: `pods.go` (column adapter derived from Kubernetes printer v1.35.0, license attribution kept in-source), `apps.go` (owner-based grouping: ReplicaSet→Deployment, suffixed Job→CronJob), `logs.go`.
- `internal/kubeconfig` — reading contexts out of kubeconfig.
- `internal/ui` — Bubble Tea model. `model.go` holds state and `Update`; `view.go` renders; `table.go` holds the lipgloss palette and table/column layout.

### Things that are load-bearing

- **UID guard before exec.** The pod is re-fetched and its UID compared before any exec; for a pane session (`ui.Attach`) that happens in the pane, which starts later than the keypress. A pod with the same name but a new UID must be re-selected; no command is retried automatically onto a replacement pod.
- **`Scope.Generation`** discriminates stale async results. Out-of-order results are dropped rather than applied.
- **Prod confirmation.** Clusters with `env: prod` (or a `-prd`/`-prod` suffix on cluster or project name) require a `y` keypress before any in-container command, pane sessions included (asked before the pane opens). Logs and describe do not require it.
- **Background reads never get stdin.** Anything that could prompt for credentials must go through `internal/terminal` with the TTY released.
- **Failure keeps the old rows** marked `STALE` rather than clearing the table.

## Conventions

The existing code has a distinct, very compact style. Match it rather than
rewriting toward a more conventional Go house style:

- Short receiver and local names; `e` for errors, `c`/`m`/`p`/`r`/`s` for the obvious local.
- Related small types declared on one line (`type IO struct { In io.Reader; Out, Err io.Writer }` style, fields grouped on shared lines).
- Comments explain *why* a rule exists (stickiness, process-group choice, UID guard), not what the line does. Density is low — do not add narration.
- Errors flow as `*domain.Error` with a `Kind`; classification of kubectl stderr is best-effort and lives in `internal/kube/kubectl`.
- No persistent debug log: never add logging of kubeconfig, credentials, commands, or session contents.
- Tests are table-driven and use fake `kubectl`/`gcloud` executables rather than mocking interfaces where practical.

## Constraints

- macOS and Linux only; Windows is not supported.
- Requires a real TTY on both stdin and stdout — the app refuses to start otherwise.
- Go version is pinned in `go.mod` (currently 1.26.1) and `GOTOOLCHAIN=local`, so the local toolchain must satisfy it.
- `kubectl` and (for GKE cluster selection) `gcloud` must be on `PATH` or passed via `--kubectl` / `--gcloud`.
