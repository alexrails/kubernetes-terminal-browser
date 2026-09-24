//go:build screenshots

package ui

import (
	tea "charm.land/bubbletea/v2"
	"context"
	core "k8s.io/api/core/v1"
	"kubernetes-terminal-browser/internal/config"
	"kubernetes-terminal-browser/internal/gcloud"
	"kubernetes-terminal-browser/internal/kube/kubectl"
	"kubernetes-terminal-browser/internal/kubeconfig"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestScreenshots writes the README screens, with fictional data only, as
// ANSI text into $KTB_SCREENS_DIR; `make screenshots` renders them with freeze.
func TestScreenshots(t *testing.T) {
	dir := os.Getenv("KTB_SCREENS_DIR")
	if dir == "" {
		t.Skip("KTB_SCREENS_DIR not set")
	}
	cfg := config.Defaults()
	cfg.Clusters = []config.Cluster{
		devCluster, prodCluster,
		{Name: "blog-europe-west1-dev", Region: "europe-west1", Project: "acme-blog-dev"},
		{Name: "blog-us-central1-prd", Region: "us-central1", Project: "acme-blog-prd"},
		{Name: "chat-asia-southeast1-stg", Region: "asia-southeast1", Project: "acme-chat-stg", Namespace: "chat-stg"},
	}
	m := New(context.Background(), kubectl.Client{Timeout: time.Second}, gcloud.Client{}, cfg, "", "", "ktb v0.1.0 · kubectl v1.36.1")
	m.Update(tea.WindowSizeMsg{Width: 96, Height: 10})
	write(t, dir, "clusters", m)

	m.selectContext(kubeconfig.Context{Name: devCluster.ContextName(), Namespace: devCluster.Namespace})
	crash := deployPod("mailer", "5d8f7c9b6d", "k2x9q", "app")
	crash.Status.ContainerStatuses[0] = core.ContainerStatus{Name: "app", RestartCount: 7, State: core.ContainerState{Waiting: &core.ContainerStateWaiting{Reason: "CrashLoopBackOff"}}}
	crash.Status.Conditions = nil
	podsResult(m,
		deployPod("admin-web", "587dcb6845", "b7tqm", "app", "proxy"), deployPod("admin-web", "587dcb6845", "x4lcz", "app", "proxy"),
		deployPod("billing-api", "6c9d4f7b85", "p2wvn", "app"),
		deployPod("mailer", "5d8f7c9b6d", "r8mzt", "app"), crash,
		deployPod("storefront", "7f6b8d9c44", "h5jkd", "app"), deployPod("storefront", "7f6b8d9c44", "q9nwe", "app"), deployPod("storefront", "7f6b8d9c44", "z3cpa", "app"))
	m.lastSuccess = time.Date(2026, 1, 1, 10, 24, 5, 0, time.Local)
	m.Update(tea.WindowSizeMsg{Width: 96, Height: 9})
	m.move(1)
	write(t, dir, "applications", m)
	m.Update(tea.WindowSizeMsg{Width: 96, Height: 11})
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	write(t, dir, "actions", m)
}
func write(t *testing.T, dir, name string, m *Model) {
	if e := os.WriteFile(filepath.Join(dir, name+".ansi"), []byte(m.View().Content), 0o644); e != nil {
		t.Fatal(e)
	}
}
