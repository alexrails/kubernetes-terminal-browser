# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[Semantic Versioning](https://semver.org/spec/v2.0.0.html): a release is a git
tag `vX.Y.Z`, and `ktb --version` prints it.

## [Unreleased]

## [0.1.1] - 2026-09-24

### Added

- README screenshots, rendered from fictional data; `make screenshots`
  regenerates them (needs `freeze`).

### Changed

- README restructured and shortened: quick start, one section per topic.

## [0.1.0] - 2026-09-24

### Added

- Terminal UI to browse clusters, namespaces, applications and pods, and to open
  a shell, a Rails console or a custom action in a container.
- Bookmarks (targets) in `clusters.yaml`: `ktb NAME` opens an application's live
  pod and runs its action in one step.
- GKE clusters from `clusters.yaml`; `gcloud get-credentials` runs only when the
  context is missing from kubeconfig.
- Without clusters in `clusters.yaml`, the cluster list is built from kubeconfig's
  GKE contexts; `ctrl+f` finds more through `gcloud` in known projects or by a
  typed project ID prefix.
- Pods grouped by application (Deployment, StatefulSet, CronJob, Job), fuzzy
  type-to-search, logs viewer and `describe` with pod events.
- Prod confirmation before any command in a container on prod clusters.
- Manual access mode for contexts with interactive credential plugins.
- `ktb --version`; the version comes from the git tag at build time.
- `make install` builds and installs into `$PREFIX/bin` (default `~/.local/bin`).
