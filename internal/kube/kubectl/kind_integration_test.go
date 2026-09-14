//go:build integration

package kubectl

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"kubernetes-terminal-browser/internal/config"
	"kubernetes-terminal-browser/internal/domain"
	"kubernetes-terminal-browser/internal/kube"
	"kubernetes-terminal-browser/internal/presentation"
	"kubernetes-terminal-browser/internal/process"
	"kubernetes-terminal-browser/internal/terminal"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestKindOperationsAndLimitedRBAC(t *testing.T) {
	source, bin := os.Getenv("KTB_INTEGRATION_KUBECONFIG"), os.Getenv("KTB_INTEGRATION_KUBECTL")
	if source == "" || bin == "" {
		t.Skip("set dedicated KTB_INTEGRATION_KUBECONFIG and KTB_INTEGRATION_KUBECTL")
	}
	source, e := filepath.Abs(source)
	if e != nil {
		t.Fatal(e)
	}
	bin, e = filepath.Abs(bin)
	if e != nil {
		t.Fatal(e)
	}
	ctx := context.Background()
	s := domain.Scope{Kubeconfig: source, Context: "kind-ktb-validation", Namespace: "ktb-validation"}
	c := Client{Executable: bin, Timeout: 10 * time.Second}
	pods, e := c.Pods(ctx, s)
	if e != nil {
		t.Fatal(e)
	}
	if len(pods) < 3 {
		t.Fatalf("expected fixtures, got %d pods", len(pods))
	}
	table := process.Run(ctx, bin, append(c.Flags(s, true), "get", "pods", "--no-headers"), nil)
	if table.Err != nil {
		t.Fatalf("kubectl table: %v %s", table.Err, table.Stderr)
	}
	rows := map[string][]string{}
	for _, line := range strings.Split(string(table.Stdout), "\n") {
		f := strings.Fields(line)
		if len(f) >= 5 {
			rows[f[0]] = f
		}
	}
	now := time.Now()
	for i := range pods {
		p := &pods[i]
		r := presentation.Pod(p, now)
		f := rows[p.Name]
		if len(f) < 5 || r.Ready != f[1] || r.Status != f[2] || r.Restarts != f[3] {
			t.Errorf("%s: adapter %+v kubectl %q", p.Name, r, f)
		}
		if r.Age == "<unknown>" {
			t.Errorf("missing age for %s", p.Name)
		}
	}
	var running, completed domain.PodRef
	for _, p := range pods {
		r := domain.PodRef{Scope: s, Name: p.Name, UID: string(p.UID)}
		if p.Name == "running" {
			running = r
		}
		if p.Name == "completed" {
			completed = r
		}
	}
	if running.UID == "" || completed.UID == "" {
		t.Fatal("fixtures missing")
	}
	if _, e = c.Pod(ctx, running); e != nil {
		t.Fatal(e)
	}
	if _, e = c.Describe(ctx, running); e != nil {
		t.Fatal(e)
	}
	if _, e = c.Events(ctx, running); e != nil {
		t.Fatal(e)
	}
	changed := running
	changed.UID = "different"
	_, e = c.Pod(ctx, changed)
	var de *domain.Error
	if !errors.As(e, &de) || de.Kind != domain.Replaced {
		t.Fatalf("UID guard: %v", e)
	}
	var log bytes.Buffer
	completedContainer := domain.ContainerRef{Pod: completed, Name: "job", Kind: "regular"}
	if e = c.Logs(ctx, completedContainer, kube.LogsOptions{Tail: 200}, &log); e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(log.String(), "done") {
		t.Fatalf("logs: %q", log.String())
	}
	var output bytes.Buffer
	runningContainer := domain.ContainerRef{Pod: running, Name: "app", Kind: "regular"}
	action := config.Action{ID: "test", Label: "Test", Argv: []string{"sh", "-c", "printf integration-ok"}}
	if e = c.Exec(ctx, runningContainer, action, terminal.IO{Out: &output, Err: &output}); e != nil {
		t.Fatal(e)
	}
	if output.String() != "integration-ok" {
		t.Fatalf("exec: %q", output.String())
	}

	// An isolated service account can list pods but cannot get pods or namespaces.
	tokenResult := process.Run(ctx, bin, append(c.Flags(s, true), "create", "token", "list-only", "--duration=10m"), nil)
	if tokenResult.Err != nil {
		t.Fatalf("token request: %v %s", tokenResult.Err, tokenResult.Stderr)
	}
	raw := process.Run(ctx, bin, []string{"--kubeconfig=" + source, "config", "view", "--raw", "-o=json"}, nil)
	if raw.Err != nil {
		t.Fatalf("read test cluster connection: %v", raw.Err)
	}
	var base struct {
		Clusters []struct {
			Name    string `json:"name"`
			Cluster struct {
				Server string `json:"server"`
				CA     string `json:"certificate-authority-data"`
			}
		} `json:"clusters"`
	}
	if e = json.Unmarshal(raw.Stdout, &base); e != nil || len(base.Clusters) == 0 {
		t.Fatalf("test connection parse: %v", e)
	}
	limited := map[string]any{"apiVersion": "v1", "kind": "Config", "clusters": []any{map[string]any{"name": "kind", "cluster": map[string]any{"server": base.Clusters[0].Cluster.Server, "certificate-authority-data": base.Clusters[0].Cluster.CA}}}, "users": []any{map[string]any{"name": "limited", "user": map[string]any{"token": strings.TrimSpace(string(tokenResult.Stdout))}}}, "contexts": []any{map[string]any{"name": "limited", "context": map[string]any{"cluster": "kind", "user": "limited", "namespace": s.Namespace}}}, "current-context": "limited"}
	data, e := json.Marshal(limited)
	if e != nil {
		t.Fatal(e)
	}
	limitedPath := filepath.Join(t.TempDir(), "limited-kubeconfig.json")
	if e = os.WriteFile(limitedPath, data, 0600); e != nil {
		t.Fatal(e)
	}
	limitedScope := domain.Scope{Kubeconfig: limitedPath, Context: "limited", Namespace: s.Namespace}
	list, e := c.Pods(ctx, limitedScope)
	if e != nil || len(list) < 3 {
		t.Fatalf("list-only pods: %d, %v", len(list), e)
	}
	if _, e = c.Namespaces(ctx, limitedScope); !errors.As(e, &de) || de.Kind != domain.Forbidden {
		t.Fatalf("namespaces should be optional and denied: %v", e)
	}
	limitedPod := running
	limitedPod.Scope = limitedScope
	if _, e = c.Pod(ctx, limitedPod); !errors.As(e, &de) || de.Kind != domain.Forbidden {
		t.Fatalf("get pods should be denied: %v", e)
	}
}
