# Isolated Kubernetes validation

Use a disposable kind cluster and a dedicated kubeconfig file. Never point this script at a production context. The checks below are a matrix for the release criteria; local unit and PTY tests do not replace them.

1. Install kind and create a cluster using a dedicated file: `kind create cluster --name ktb-validation --kubeconfig "$PWD/.cache/kind-kubeconfig"`.
2. Set `KUBECONFIG="$PWD/.cache/kind-kubeconfig"` for each kubectl command. Record `kubectl version -o=json` and the kind node image digest.
3. Create a namespace with a Running single-container pod, a multi-container pod, a completed pod, an init/sidecar pod, and a pod without `/bin/sh`. Repeat with a namespace that has no pods.
4. Create service accounts with: full namespace access; pods list/get but no namespaces list/get; list pods only; logs but no exec; describe with events forbidden. Use a separate test kubeconfig/context for each role and run `ktb` with `--kubeconfig`.
5. Compare the five pod columns with `kubectl get pods` on stable fixtures. Record the server and client versions. Allow only elapsed-time movement in AGE and restart recency.
6. Recreate a pod with the same name and a new UID between selection and action. Verify no exec begins and re-selection is required.
7. Exercise login using a test ExecCredential plugin with `Always`, `IfAvailable`, expired credentials and a forced error. Verify no background stdin, one background probe after successful foreground retry, and a stable ManualOnly state after another failure.
8. Run exec and follow logs through SSH and tmux on macOS and Linux; test resize, Ctrl+C/Ctrl+D, SIGTERM to the app PID, network loss, and process cleanup. Record terminal modes before and after.
9. Run a 1,000-pod fixture. Measure kubectl/plugin startup, JSON size and parse time, full refresh time, search/navigation latency, and long-log memory. This evidence determines whether to retain kubectl polling or switch reads to client-go.

The checked-in fixture can be created with `kubectl --kubeconfig "$PWD/.cache/kind-kubeconfig" --context kind-ktb-validation apply -f testdata/kind-fixtures.yaml`. Wait for `running` and `multi` to become Ready and `completed` to reach Succeeded. The Go integration test needs absolute paths because Go runs each package from its own directory:

```sh
KTB_INTEGRATION_KUBECONFIG="$PWD/.cache/kind-kubeconfig" \
KTB_INTEGRATION_KUBECTL="$PWD/.tools/kubectl" \
  ./scripts/go test -tags=integration -run TestKindOperationsAndLimitedRBAC -v ./internal/kube/kubectl
python3 scripts/kind_pty_smoke.py
```

The tested local matrix is macOS arm64, kind v0.32.0, Kubernetes API server v1.36.1, kubectl v1.36.1, and the app's 1.35.0 printer adapter. The integration test covers live pod columns (except exact AGE at a boundary), get/describe/events, logs, exec, UID rejection, and a service account allowed to list pods but forbidden from getting pods or listing namespaces. The live PTY test covers shell handoff and describe. This does not establish parity across every supported Kubernetes minor version.

The automated local suite also covers argv, UID guard, printer fixtures, cancellation of a grandchild process, authentication state, out-of-order UI results, bounded log rendering, and fake-kubectl PTY paths. The other roles and SSH/tmux/provider environments above remain a release checklist, not a claim that they have passed.
