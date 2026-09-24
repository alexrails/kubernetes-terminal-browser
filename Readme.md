# ktb — a terminal browser for Kubernetes

`ktb` is a local terminal application for browsing Kubernetes contexts, namespaces, pods and containers. On start it shows bookmarks (targets) and GKE clusters from `clusters.yaml`. A bookmark opens a Rails console or a shell in the current pod of an application in one step. A cluster leads step by step: namespace → application → pod → action. `gcloud container clusters get-credentials` runs only when the cluster's context is not in kubeconfig yet. From the application you can also open a shell in a container, view pod logs and description, or run your own command. It needs no server, database or in-cluster components.

The application never edits or deletes Kubernetes resources itself. Selecting a GKE cluster updates the local kubeconfig via `gcloud get-credentials`. **Commands you explicitly run inside a container can change its state or data.** `ktb` never runs such commands automatically and never retries them after a failure.

## Features

- **Bookmarks (targets)** in `~/.config/ktb/clusters.yaml`: cluster + namespace + application (+ container and action). `ktb admin-shop-dev`, or Enter on a bookmark, opens a Rails console in the application's live pod, whatever the pod hash is after a deploy.
- **Prod protection:** clusters with `env: prod` (by default, those with a `-prd`/`-prod` suffix) carry a red `PROD` badge, and every command in a container there needs confirmation with `y`.
- **No needless `gcloud`:** if the context `gke_<project>_<region>_<cluster>` is already in kubeconfig, the cluster opens at once; `ctrl+r` on a cluster forces a credentials refresh.
- **Applications instead of pods:** pods are grouped by Deployment / StatefulSet / CronJob / Job with `PODS`, `STATUS`, `RESTARTS`, `AGE` columns. `Tab` switches to the flat pod list.
- **Type-to-search, fuzzy:** `adweb` finds `admin-web`; best matches come first.
- **Action menu in a popup** over the list: Shell, Rails console and any actions from `config.yaml`; `Tab` in the menu switches the container.
- Logs (tail, follow, previous, scrolling), `describe pod` with that pod's events.
- Manual mode for contexts with an interactive credential plugin, and background reads stop after an authorization failure.

The application targets macOS and Linux. Windows is not supported. A full-screen terminal is required over SSH or tmux as well; thorough testing of those environments is not finished yet.

## Requirements

1. **Go 1.26.1 or newer** — to build from source. A prebuilt binary does not need Go.
2. **`kubectl` in `PATH`** — needed on every run. Its version must be compatible with the API server; Kubernetes allows at most one minor version of skew. Instructions: [macOS](https://kubernetes.io/docs/tasks/tools/install-kubectl-macos/) and [Linux](https://kubernetes.io/docs/tasks/tools/install-kubectl-linux/).
3. **`gcloud` in `PATH`** — needed to select a GKE cluster. Without it you can still open an existing context with `ctrl+k`. The account must be logged in and allowed to get credentials for the selected cluster.
4. **Cluster access through kubeconfig** — the startup list of GKE clusters is set in `~/.config/ktb/clusters.yaml`; without it the list is built from kubeconfig's GKE contexts, and `ctrl+f` adds clusters found through `gcloud` (in known projects or by project ID prefix). Existing non-GKE and GKE contexts are always available with `ctrl+k`.
5. **A real TTY on stdin and stdout** — running through a pipe or a non-terminal CI job will not open the interface.
6. `make` for the build commands below. Without `make`, use `./scripts/go build -trimpath -o bin/ktb ./cmd/ktb`.

Check the environment before installing the application:

```sh
go version
kubectl version --client
gcloud version
kubectl config get-contexts
kubectl --context CONTEXT_NAME --namespace default get pods
```

The last command must work for the context you are going to use with `ktb`. If it asks you to log in, finish setting up access before starting the application. The application does not need permission to see all namespaces: a known namespace with `list pods` is enough.

## Installation

### macOS

Install Go and kubectl. With Homebrew:

```sh
brew install go kubectl
go version
kubectl version --client
```

Without Homebrew, install Go following the [official instructions](https://go.dev/doc/install) and kubectl following the [Kubernetes instructions for macOS](https://kubernetes.io/docs/tasks/tools/install-kubectl-macos/). Make sure the Go version is not older than the one in `go.mod`. If Homebrew installed a kubectl incompatible with your cluster, take a suitable binary version from the same Kubernetes instructions.

Go to the project root and build the application:

```sh
cd /path/to/kubernetes-terminal-browser
make build
./bin/ktb --help
```

### Linux

You can use your package manager if it provides Go 1.26.1+ and the kubectl version you need. Alternatively, install the binaries into your home directory without `sudo`. In the example below, replace `amd64` with `arm64` for ARM64 and pick a kubectl version compatible with your cluster (`v1.36.1` is only an example):

```sh
GO_VERSION=1.26.1
ARCH=amd64
mkdir -p "$HOME/.local/opt/go-$GO_VERSION" "$HOME/.local/bin"
curl -fL "https://go.dev/dl/go${GO_VERSION}.linux-${ARCH}.tar.gz" -o "/tmp/go${GO_VERSION}.linux-${ARCH}.tar.gz"
tar -xzf "/tmp/go${GO_VERSION}.linux-${ARCH}.tar.gz" -C "$HOME/.local/opt/go-$GO_VERSION" --strip-components=1

KUBECTL_VERSION=v1.36.1
curl -fL "https://dl.k8s.io/release/${KUBECTL_VERSION}/bin/linux/${ARCH}/kubectl" -o /tmp/ktb-kubectl
curl -fL "https://dl.k8s.io/release/${KUBECTL_VERSION}/bin/linux/${ARCH}/kubectl.sha256" -o /tmp/ktb-kubectl.sha256
printf '%s  %s\n' "$(cat /tmp/ktb-kubectl.sha256)" /tmp/ktb-kubectl | sha256sum --check
install -m 755 /tmp/ktb-kubectl "$HOME/.local/bin/kubectl"

export PATH="$HOME/.local/opt/go-$GO_VERSION/bin:$HOME/.local/bin:$PATH"
```

Save these `PATH` additions in your shell configuration and open a new terminal. Detailed installation and upgrade options are in the [Go](https://go.dev/doc/install) and [Kubernetes](https://kubernetes.io/docs/tasks/tools/install-kubectl-linux/) instructions. Check the versions and build the application:

```sh
go version
kubectl version --client
cd /path/to/kubernetes-terminal-browser
make build
./bin/ktb --help
```

If your package manager installed an old Go, upgrade it before building. `scripts/go` uses the local compiler `.tools/go/bin/go` if it is already in the project directory, and the system `go` otherwise. Go dependencies are downloaded on the first build, so it needs access to the Go module proxy.

### Installing into `PATH`

After building you can run `./bin/ktb` straight from the project. To make `ktb` work from any directory:

```sh
make install                   # builds and installs into ~/.local/bin/ktb
make install PREFIX=/usr/local # or another prefix: $PREFIX/bin/ktb
```

Run the same command to upgrade after `git pull`. It replaces the binary with a new file rather than overwriting it in place, since macOS kills a signed binary overwritten in place (exit code 137). It then reports whether the install directory is in `PATH` and whether `kubectl` and `gcloud` are found; add `~/.local/bin` to `PATH` if asked and open a new terminal. `make install` does not install `kubectl` and `gcloud`: they still have to be in `PATH` or passed with the corresponding flags.

## First run

From the project root:

```sh
./bin/ktb
```

Nothing has to be configured up front. Clusters and bookmarks are set in `~/.config/ktb/clusters.yaml` (or `$XDG_CONFIG_HOME/ktb/clusters.yaml`), but the file is optional: without it clusters come from kubeconfig and `ctrl+f` (see below). Create it when you want to pin your own cluster list or add bookmarks — see [Clusters: `clusters.yaml`](#clusters-clustersyaml).

The start screen is the bookmark (target) list; `Tab` switches to the cluster list. Without bookmarks, clusters are shown right away; without clusters too, kubeconfig contexts.

**Clusters without `clusters.yaml`.** If `clusters.yaml` lists no clusters (or does not exist), the list is built automatically from kubeconfig contexts named `gke_<project>_<region>_<cluster>` — that is, clusters `get-credentials` has already run for. `ctrl+f` searches for clusters through `gcloud` and adds the missing ones:

- **with nothing typed** — in already known projects: from `clusters.yaml`, kubeconfig's GKE contexts and all `gcloud config configurations`. Fast and needs no setup;
- **with a prefix** — type the start of a project ID on the cluster screen (for example `acme-`) and press `ctrl+f`: this runs `gcloud projects list --filter=projectId:acme-*` (the filter is applied by the server), then `gcloud container clusters list` for each project found, up to 16 in parallel. If the prefix matches more than 300 projects, the application asks for a longer one.

All of the account's projects are never scanned without a prefix: there can be thousands, one gcloud call each. The request runs in the background with `--quiet`: if a login is needed, an error is shown — run `gcloud auth login` and press `ctrl+f` again. Projects where listing fails (API disabled, no permission) are skipped. Zonal clusters are not shown. Found clusters are not saved to the file; prod is detected by the `-prd`/`-prod` suffix.

**Bookmark.** Enter on a bookmark (or `ktb BOOKMARK_NAME` from the shell) opens the cluster, finds the bookmark's application in the namespace and picks its best pod: Running and Ready, newest first. If the bookmark sets `action`, that action runs at once; otherwise the action menu opens.

```sh
ktb admin-shop-dev     # straight to a Rails console in admin-web on shop-europe-west1-dev
```

**Cluster.** Enter on a cluster first looks for the context `gke_<project>_<region>_<cluster>` in kubeconfig. If it exists, `gcloud` is not run. Otherwise the TUI temporarily hands the terminal to a command like:

```sh
gcloud container clusters get-credentials shop-europe-west1-dev --region europe-west1 --project acme-shop-dev
```

Then the cluster's namespace list is shown. The namespace from `clusters.yaml` is selected, otherwise the context's namespace or `default`. Enter opens the applications of the selected namespace; kubeconfig is not changed by this. Without `list namespaces` permission, just type the namespace name and press Enter. `ctrl+r` on a cluster forces `get-credentials`, for example when the cluster was recreated.

**Applications and pods.** Enter on an application with one pod opens the action menu at once. With several pods, their list opens. The menu works on the container `kubectl exec` would pick without `-c` (the `kubectl.kubernetes.io/default-container` annotation, otherwise the first container). `Tab` in the menu switches the container; digits `1`–`9` run an action immediately:

- **Shell (/bin/sh)** — just get into the pod;
- **Rails console (bundle exec rails console)** — start a Rails console right away.

On a prod cluster a confirmation window appears before running: `y` runs, any other key cancels.

Common invocations:

```sh
# Start in a specific namespace
./bin/ktb --namespace payments

# Use a separate kubeconfig file; the original current-context is not changed
./bin/ktb --kubeconfig /path/to/test-kubeconfig

# Pass a list of files by the standard kubectl rules on macOS/Linux
KUBECONFIG="$HOME/.kube/base:$HOME/.kube/extra" ./bin/ktb

# Disable automatic refresh
./bin/ktb --refresh 0

# Refresh every 10 seconds and wait up to 30 seconds for short reads
./bin/ktb --refresh 10s --timeout 30s

# Use a kubectl that is not in PATH
./bin/ktb --kubectl /path/to/kubectl

# Use a gcloud that is not in PATH
./bin/ktb --gcloud /path/to/gcloud
```

Without `--kubeconfig`, kubectl and gcloud resolve `KUBECONFIG` and `~/.kube/config` themselves. With an explicit `--kubeconfig`, the application passes that path to gcloud in `KUBECONFIG`, so credentials are written to the same file. `ktb` never calls `kubectl config use-context`; the current-context change on cluster selection is standard `gcloud get-credentials` behavior. After you pick a namespace by hand, the application remembers it per context until it exits.

### Flags

| Flag | Purpose | Default |
| --- | --- | --- |
| `--kubeconfig PATH` | Explicit kubeconfig file | kubectl resolution: `KUBECONFIG` or `~/.kube/config` |
| `--namespace NAME` | Initial namespace | The context's namespace or `default` |
| `--config PATH` | Application settings YAML | `$XDG_CONFIG_HOME/ktb/config.yaml` or `~/.config/ktb/config.yaml` |
| `--clusters PATH` | YAML list of GKE clusters for the start screen | `$XDG_CONFIG_HOME/ktb/clusters.yaml` or `~/.config/ktb/clusters.yaml` |
| `--kubectl PATH` | Path or name of the kubectl executable | `kubectl` |
| `--gcloud PATH` | Path or name of the gcloud executable | `gcloud` |
| `--refresh DURATION` | Pod refresh interval; `0` disables the timer | `5s` or the config value |
| `--timeout DURATION` | Deadline for short read requests | `15s` or the config value |
| `--check-config` | Validate the settings and exit without opening the TUI | Off |
| `--version` | Print the ktb version and exit | — |
| `--help` | Show flag help | — |

The only positional argument is a bookmark name: `ktb [flags] [bookmark]`.

`--refresh` and `--timeout` override the YAML fields of the same name. `refresh` accepts `0` or an interval of at least `1s`; `timeout` accepts `1s` to `5m`. The short read timeout does not apply to an interactive shell.

## Using the interface

The route: a **bookmark** — straight to a console, or **cluster → namespace → application → pod → action**. The bottom line always shows the current screen's keys; `?` opens full help.

**Type-to-search.** On any list just start typing — the list is filtered fuzzily (`adweb` → `admin-web`), the best matches rise to the top and get selected. `Backspace` deletes a character, `ctrl+u` clears, `Esc` first clears the filter and the next `Esc` goes back. That is why commands use `ctrl` keys:

| Key | Where | Action |
| --- | --- | --- |
| letters, digits | Lists | Filter (fuzzy search) |
| `↑` / `↓`, `PgUp` / `PgDn`, `Home` / `End` | Lists | Move the selection |
| `Enter` | Lists | Open; on an application or pod, the action menu |
| `Tab` | Start / pods / menu | Bookmarks ⇄ clusters · applications ⇄ pods · next container |
| `Esc` | Everywhere | Clear the filter, then go back; close a window |
| `ctrl+s` | Pods, containers | Shell in the selected container |
| `ctrl+l` | Pods, containers | Logs |
| `ctrl+d` | Pods, containers | Pod describe and events |
| `ctrl+r` | Lists | Refresh; on a cluster, run `get-credentials` again |
| `ctrl+g` | Everywhere | Start screen (bookmarks / clusters) |
| `ctrl+k` | Everywhere | kubeconfig context list |
| `ctrl+f` | Bookmarks, clusters, contexts | Find GKE clusters through `gcloud` in known projects; after a typed prefix, in projects with that prefix |
| `ctrl+n` | Pods | Change namespace |
| `ctrl+b` | Pods | Re-enable background reads after manual mode |
| `1`–`9`, `Tab` | Action menu | Run an action by number; switch the container |
| `y` | Prod confirmation | Run; any other key cancels |
| `?` | Everywhere | Help |
| `ctrl+c` | Everywhere | Quit |

The header shows the path `cluster › namespace › application › pod / container`, plus a red `PROD` badge for a prod cluster. Below the header are counters, the refresh time and an animated indicator while a request is running. Errors are shown in red above the key line.

The selection survives refreshes: by name for an application, by UID for a pod. A pod with the same name and a new UID has to be selected again. After a failed refresh the previous rows stay, marked `STALE`.

### Applications

Pods are grouped by owner: a ReplicaSet without `pod-template-hash` → Deployment, a Job with a numeric suffix → CronJob, as well as StatefulSet, DaemonSet and Job. A pod without an owner is an application of its own. The `PODS` column shows ready/active pods (`1/2` is highlighted in yellow), and for finished CronJobs the number of completed runs. `STATUS` lists the pod statuses, worst first: `CrashLoopBackOff, Running ×2`. `Tab` switches to the flat list of all pods.

### Choosing a namespace

`ctrl+n` on the pod screen opens the namespace list. Type part of a name to filter, or a full name and press Enter: if that name is not in the list (for example, without `list namespaces` permission), the application opens the namespace with that name anyway. An empty pod list does not prove the namespace exists: the application reports that uncertainty explicitly.

### Shell and other commands

Enter on an application or pod opens the action menu; `ctrl+s` opens a shell right away. If a pod has several containers, `ctrl+s`/`ctrl+l` open the container list first. Exec does not start in a finished or non-running container; logs may still be available.

Before every exec the application fetches the pod again and compares its UID. It then hands the TTY to a local `kubectl exec` and prints a line saying what runs where. Normal input, Ctrl+C and Ctrl+D work inside the container. After exit the application returns to the list with the previous selection. A non-zero exit code never triggers an automatic attempt with another command. Action arguments are passed separately, without a local shell.

### Logs

Press `ctrl+l` on an application, pod or container. By default **the last 200 lines with timestamps, without follow** are requested. The built-in viewer supports:

| Key | Action |
| --- | --- |
| `f` | Toggle follow and reopen the stream |
| `p` | Toggle previous, for the container's previous instance |
| `t` | Cycle the tail size: `50 → 200 → 1000 → 50` |
| `↑` / `↓`, `PageUp` / `PageDown` | Scroll the output |
| `r` | Reconnect with the current options; in manual mode, run `kubectl logs` in the terminal (Enter does the same) |
| `Esc` | Close the viewer and end its kubectl process |

Scrolling up stops auto-scrolling to the last line; scrolling back down resumes it. The buffer is limited to 10,000 lines and 10 MiB; old output may be truncated. In the built-in viewer, control characters from logs are not executed by the terminal.

For a context in manual mode (`ManualOnly`, including `Always`), `ctrl+l` opens the log screen without a cluster request. Set `f`, `p` and `t`, then press `r` or Enter: the TUI hands the terminal to `kubectl logs`, and after it ends (for follow, after Ctrl+C) asks you to press Enter and returns to the same screen. Output in this mode goes straight to the terminal, as with a manual `kubectl logs`: built-in scrolling is not available and control characters are not filtered.

### Describe and events

Press `ctrl+d` on a pod. The base description is fetched separately from the event list. If RBAC forbids events, the description still opens and the events error is shown at the bottom. Use the arrows and PageUp/PageDown to scroll, `r` to refresh and Esc to go back.

## Configuring clusters, actions and refresh

### Clusters: `clusters.yaml`

The start-screen cluster list and the bookmarks live in `~/.config/ktb/clusters.yaml` (or `$XDG_CONFIG_HOME/ktb/clusters.yaml`; another path with `--clusters`). The file is optional. Create it and write **your own** clusters into it:

```sh
mkdir -p "${XDG_CONFIG_HOME:-$HOME/.config}/ktb"
"${EDITOR:-vi}" "${XDG_CONFIG_HOME:-$HOME/.config}/ktb/clusters.yaml"
```

`clusters.example.yaml` in the repository is only a format sample with fictional clusters; do not copy it as is. If the file lists at least one cluster, the automatic list from kubeconfig is not built — only the file's clusters are shown (`ctrl+f` still adds the ones found through `gcloud`). You can look up the exact `name`, `region` and `project` of clusters with `ctrl+f` or `gcloud container clusters list --project PROJECT`.

Format:

```yaml
clusters:
  - name: shop-europe-west1-dev     # cluster name for get-credentials
    region: europe-west1
    project: acme-shop-dev
    namespace: shop-dev             # optional: selected in the namespace list
  - name: shop-us-central1-prd
    region: us-central1
    project: acme-shop-prd
    env: prod                       # optional: prod or dev

targets:
  - name: admin-shop-dev            # name for the list and for `ktb admin-shop-dev`
    cluster: shop-europe-west1-dev  # a name from clusters
    namespace: shop-dev             # optional, otherwise the cluster's namespace
    app: admin-web                  # Deployment / StatefulSet / CronJob…
    container: app                  # optional, otherwise the default container
    action: rails-console           # optional: an action id; without it, the menu
```

Each `clusters` entry maps to exactly one `gcloud container clusters get-credentials NAME --region REGION --project PROJECT` command; the same triple cannot repeat. `region` must be a region (not a zone), `namespace` a valid DNS name. `env` accepts `prod` or `dev`; without it, a cluster is prod when its cluster or project name ends in `-prd` or `-prod`. On prod, every command in a container asks for confirmation; logs and describe do not.

`targets` refer to a cluster by `name`. `app` is the name of the pods' owner: Deployment, StatefulSet, DaemonSet, CronJob or Job, as in the `APP` column. `action` must match the `id` of an action in `config.yaml`. A bookmark name may contain letters, digits, `.`, `_` and `-`.

Unknown fields are errors. Another file can be given with `--clusters`; when the flag is set explicitly, the file must exist.

### Settings and actions: `config.yaml`

The settings file is optional. Without it `refresh: 5s`, `timeout: 15s` and two actions are used: Shell (`/bin/sh`, `id: sh`) and Rails console (`bundle exec rails console`, `id: rails-console`).

```sh
cp config.example.yaml "${XDG_CONFIG_HOME:-$HOME/.config}/ktb/config.yaml"
./bin/ktb --check-config
```

Example:

```yaml
refresh: 10s
timeout: 30s
actions:
  - id: sh
    label: Shell (/bin/sh)
    argv: ["/bin/sh"]
    stdin: true
    tty: true
  - id: rails-console
    label: Rails console (bundle exec rails console)
    argv: ["bundle", "exec", "rails", "console"]
    stdin: true
    tty: true
  - id: version
    label: Application version
    argv: ["cat", "/app/VERSION"]
    stdin: false
    tty: false
```

The `actions` list in YAML **replaces** the default list. `ctrl+s` runs the action with `id: sh`, or `/bin/sh` when there is none; to use another shell, add an `sh` action with the `argv` you need. `id` and `label` must be unique, `argv` a non-empty array of strings; `tty: true` requires `stdin: true`. Do not write the whole command as one string: `argv: ["bundle", "exec", "rails", "console"]` passes four separate arguments. For shell syntax inside the container, ask for it explicitly, for example `argv: ["sh", "-lc", "echo ready"]`.

Validating the files without connecting to a cluster:

```sh
./bin/ktb --config /path/to/config.yaml --clusters /path/to/clusters.yaml --check-config
```

## Minimum Kubernetes permissions

| Feature | Permissions needed |
| --- | --- |
| Pod table | `list pods` in the selected namespace |
| Pod re-check and base describe | `get pods` |
| Logs | `get pods` and `get pods/log` |
| Exec | `get pods` and `pods/exec` permissions for the transport your kubectl version uses |
| Namespace list | `list namespaces`, optional |
| Events in describe | `list events`, optional |

The application makes no extra global `auth can-i` preflight requests. For example, without `list namespaces` you can type a name by hand; without events the base description is still available. The verb `pods/exec` needs depends on the kubectl transport and the cluster version, so check the permission with a real test request.

## Authorization modes

With `interactiveMode: Always` on the credential plugin, the application switches to manual mode **before the first API request**. Press `ctrl+r` on the pod screen: the TUI temporarily hands the terminal to kubectl so the plugin can ask for a login. Automatic polling is off in this mode.

For other contexts, requests first run in the background without stdin. If credentials are invalid, automatic retries stop. Press `ctrl+r` for one explicit foreground attempt. After a successful login the application tries one background request; if it again needs an interactive login, the context stays in `ManualOnly`: further `ctrl+r` presses do foreground reads only and start no new background checks. `ctrl+b` on the pod screen explicitly re-enables background reads, except in `Always` mode.

The access mode, when it differs from the normal `BackgroundReady`, is shown under the header: `access: NeedsAuth`, `ForegroundRetry` or `ManualOnly`. After kubeconfig changes, reload the context list with `ctrl+k` and `ctrl+r`: each context's mode is derived again from its `interactiveMode`.

## Troubleshooting

| Situation | What to check |
| --- | --- |
| `kubectl executable unavailable` | Run `kubectl version --client`; check `PATH` or pass `--kubectl /absolute/path/kubectl`. |
| `gcloud get-credentials ... executable file not found` | Run `gcloud version`; check `PATH` or pass `--gcloud /absolute/path/gcloud`. Existing contexts are available with `ctrl+k`. |
| A cluster is missing | Press `ctrl+f`, or type a project ID prefix and press `ctrl+f`, to find it through `gcloud`; or add its exact `name`, `region` and `project` to `clusters.yaml` and restart the application. |
| Bookmark: `no pods of application` | Check `app`: it is the name from the `APP` column on the application screen, not a pod name. Check the bookmark's namespace. |
| Cluster recreated, context stale | Select the cluster and press `ctrl+r`: `get-credentials` runs again. |
| `gcloud get-credentials ... failed` | Check the active account, project permissions, cluster name and region. The error is shown without an automatic retry. |
| `ktb requires a terminal` | Run the application in an interactive terminal without redirecting stdin/stdout. |
| A context is missing | Check `kubectl config get-contexts`, `KUBECONFIG` or `--kubeconfig`; press `ctrl+k`, then `ctrl+r`. |
| `Credentials` / `NeedsAuth` | Press `ctrl+r` for a foreground login or refresh access some other way. Manual mode is expected for `Always`. |
| `Forbidden` for namespaces | Type the name of a known namespace and press Enter. |
| `Forbidden` for pods, logs or exec | Compare against the permission table above for the selected context, namespace and container. Permission for one operation does not grant another. |
| `0 pods` | Check the context, namespace and search filter. An empty successful response does not confirm the namespace exists. |
| `STALE` | The last refresh failed; read the error message, then press `ctrl+r`. The old rows are kept for orientation. |
| A pod disappeared or changed UID | Refresh the list and select the pod again; the command is never run on a replacement automatically. |
| No `/bin/sh` in the container | Add an action with an executable that exists. A missing shell is often normal for minimal images. |
| Logs do not update | In the viewer press `f` for follow; `r` reopens the stream. |

The application shows kubectl's original stderr and exit code, but classifying error text is best effort. It keeps no persistent debug log of kubeconfig, credentials, commands or session contents.

## Development and testing

From the project root:

```sh
make build   # build bin/ktb for the current OS
make install # build and install into $PREFIX/bin (default ~/.local/bin)
make test    # unit tests
make race    # tests under the race detector
make vet     # Go static checks
make cross   # linux/darwin builds for amd64/arm64 into dist/

python3 scripts/pty_smoke.py       # fake gcloud/kubectl: cluster selection, namespace choice, pod actions, exec, Ctrl+C, resize, logs, SIGTERM
python3 scripts/auth_pty_smoke.py  # fake kubectl: Always (manual mode, foreground logs) and IfAvailable (one probe, then ManualOnly)
```

Run `make build` before the PTY scripts; they need Python 3 with the standard library only. [docs/integration.md](docs/integration.md) describes the kind fixtures and the integration test against a real API server. Tested locally on macOS arm64 with kind v0.32.0, API server and kubectl v1.36.1; the test kind cluster was deleted afterwards. Linux builds are produced too, but interactive use on Linux, SSH/tmux and real GKE/non-GKE credential plugins still need separate testing.

### Versioning and releases

Versions follow [Semantic Versioning](https://semver.org/); a release is a git tag `vX.Y.Z`. `make build` embeds the version from `git describe` (`v0.1.0`, or `v0.1.0-3-gabc1234-dirty` between tags), and `ktb --version` and the `?` help show it. User-visible changes go under `Unreleased` in [CHANGELOG.md](CHANGELOG.md). To release:

1. Rename `Unreleased` in `CHANGELOG.md` to the new version and date, and add an empty `Unreleased` above it.
2. Commit, then tag: `git tag -a v0.2.0 -m v0.2.0`.
3. `make install` to install the tagged build.

The pod column adapter is based on [Kubernetes printer v1.35.0](https://github.com/kubernetes/kubernetes/blob/v1.35.0/pkg/printers/internalversion/printers.go), with the license attribution kept in the source. For a wider range of server versions, check the columns against `kubectl get pods` on your pod types. With many pods and an expensive credential plugin, keep in mind that each refresh starts a separate kubectl process.
