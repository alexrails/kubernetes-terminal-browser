package ui

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"errors"
	"github.com/charmbracelet/x/ansi"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"kubernetes-terminal-browser/internal/auth"
	"kubernetes-terminal-browser/internal/config"
	"kubernetes-terminal-browser/internal/domain"
	"kubernetes-terminal-browser/internal/gcloud"
	"kubernetes-terminal-browser/internal/kube/kubectl"
	"kubernetes-terminal-browser/internal/kubeconfig"
	"strings"
	"testing"
	"time"
)

func testModel() *Model {
	return New(context.Background(), kubectl.Client{Timeout: time.Second}, gcloud.Client{}, config.Defaults(), "", "", "")
}

var (
	devCluster  = config.Cluster{Name: "shop-europe-west1-dev", Region: "europe-west1", Project: "acme-shop-dev", Namespace: "shop-dev"}
	prodCluster = config.Cluster{Name: "shop-us-central1-prd", Region: "us-central1", Project: "acme-shop-prd", Namespace: "shop-prd"}
)

func clusterModel(ts ...config.Target) *Model {
	cfg := config.Defaults()
	cfg.Clusters = []config.Cluster{devCluster, prodCluster}
	cfg.Targets = ts
	return New(context.Background(), kubectl.Client{Timeout: time.Second}, gcloud.Client{}, cfg, "", "", "")
}

// deployPod is a running, ready pod of Deployment app with the given containers.
func deployPod(app, hash, suffix string, containers ...string) core.Pod {
	yes := true
	p := core.Pod{ObjectMeta: metav1.ObjectMeta{
		Name: app + "-" + hash + "-" + suffix, UID: types.UID("uid-" + suffix),
		Labels:            map[string]string{"pod-template-hash": hash},
		OwnerReferences:   []metav1.OwnerReference{{Kind: "ReplicaSet", Name: app + "-" + hash, Controller: &yes}},
		CreationTimestamp: metav1.NewTime(time.Now().Add(-time.Hour)),
	}}
	p.Status.Phase = core.PodRunning
	p.Status.Conditions = []core.PodCondition{{Type: core.PodReady, Status: core.ConditionTrue}}
	for _, c := range containers {
		p.Spec.Containers = append(p.Spec.Containers, core.Container{Name: c})
		p.Status.ContainerStatuses = append(p.Status.ContainerStatuses, core.ContainerStatus{Name: c, Ready: true, State: core.ContainerState{Running: &core.ContainerStateRunning{}}})
	}
	return p
}
func key(r rune) tea.KeyPressMsg  { return tea.KeyPressMsg{Code: r, Text: string(r)} }
func ctrl(r rune) tea.KeyPressMsg { return tea.KeyPressMsg{Code: r, Mod: tea.ModCtrl} }
func contextsResult(m *Model, current string, cs ...kubeconfig.Context) {
	m.Update(result{generation: m.generation, id: m.request, kind: "contexts", data: kubeconfig.Snapshot{Current: current, Contexts: cs}})
}
func podsResult(m *Model, ps ...core.Pod) {
	m.Update(result{generation: m.generation, id: m.request, kind: "pods", data: ps})
}
func screenText(m *Model) string { return ansi.Strip(m.View().Content) }

func TestStartupShowsClustersWithoutReadingKubeconfig(t *testing.T) {
	m := clusterModel()
	if m.screen != clusters {
		t.Fatalf("initial screen = %s, want clusters", m.screen)
	}
	if cmd := m.Init(); cmd != nil {
		t.Fatal("cluster screen read kubeconfig before selection")
	}
	if s := screenText(m); !strings.Contains(s, "shop-europe-west1-dev") || !strings.Contains(s, "prod") {
		t.Fatalf("cluster table missing entries:\n%s", s)
	}
	if _, cmd := m.Update(ctrl('k')); cmd == nil || m.screen != contexts || !m.busy {
		t.Fatal("ctrl+k did not load the context list")
	}
}

func TestClustersDiscoveredFromKubeconfig(t *testing.T) {
	m := testModel()
	if m.screen != contexts || m.Init() == nil {
		t.Fatal("no clusters.yaml did not start by reading kubeconfig")
	}
	contextsResult(m, "", kubeconfig.Context{Name: "kind-kind"}, kubeconfig.Context{Name: prodCluster.ContextName()}, kubeconfig.Context{Name: devCluster.ContextName()})
	if m.screen != clusters || len(m.clusters) != 2 || m.clusters[0].Name != devCluster.Name || m.clusters[0].Namespace != "" {
		t.Fatalf("screen=%s clusters=%+v", m.screen, m.clusters)
	}
	m.Update(ctrl('k'))
	contextsResult(m, "", kubeconfig.Context{Name: devCluster.ContextName()})
	if m.screen != contexts || len(m.clusters) != 2 {
		t.Fatalf("reload left contexts or duplicated clusters: screen=%s clusters=%d", m.screen, len(m.clusters))
	}
}

func TestNoGKEContextsStayOnContexts(t *testing.T) {
	m := testModel()
	m.Init()
	contextsResult(m, "", kubeconfig.Context{Name: "kind-kind"})
	if m.screen != contexts || len(m.clusters) != 0 {
		t.Fatalf("screen=%s clusters=%d", m.screen, len(m.clusters))
	}
}

func TestCtrlFAddsGcloudClusters(t *testing.T) {
	m := clusterModel()
	if _, cmd := m.Update(ctrl('f')); cmd == nil || !m.busy || m.screen != clusters {
		t.Fatal("ctrl+f did not list clusters")
	}
	extra := config.Cluster{Name: "new-europe-west2-dev", Region: "europe-west2", Project: "acme-new-dev"}
	m.Update(result{generation: m.generation, id: m.request, kind: "gcloud clusters", data: found{clusters: []config.Cluster{devCluster, extra}, projects: 2}})
	if len(m.clusters) != 3 || m.clusters[2] != extra || m.clusters[0].Namespace != devCluster.Namespace || !strings.Contains(m.status, "1 new") || !strings.Contains(m.status, "type a project ID prefix") {
		t.Fatalf("clusters=%+v status=%q", m.clusters, m.status)
	}
}

func TestCtrlFWithTypedPrefixSearchesProjects(t *testing.T) {
	m := clusterModel()
	for _, r := range "zz-" {
		m.Update(key(r))
	}
	if s := screenText(m); !strings.Contains(s, "starting with zz-") || !strings.Contains(s, "search projects zz-*") {
		t.Fatalf("prefix not offered:\n%s", s)
	}
	if _, cmd := m.Update(ctrl('f')); cmd == nil || m.query != "zz-" {
		t.Fatal("ctrl+f dropped the prefix")
	}
	m.Update(result{generation: m.generation, id: m.request, kind: "gcloud clusters", data: found{projects: 3, prefix: "zz-"}})
	if !strings.Contains(m.status, "3 projects zz-*") {
		t.Fatalf("status=%q", m.status)
	}
	m.setQuery("Bad!")
	if _, cmd := m.Update(ctrl('f')); cmd != nil || !strings.Contains(m.err, "not a project ID prefix") {
		t.Fatalf("invalid prefix ran gcloud: err=%q", m.err)
	}
}

func TestCtrlFFailureWithoutClustersReturnsToContexts(t *testing.T) {
	m := testModel()
	m.Init()
	contextsResult(m, "", kubeconfig.Context{Name: "kind-kind"})
	m.Update(ctrl('f'))
	m.Update(result{generation: m.generation, id: m.request, kind: "gcloud clusters", err: errors.New("gcloud projects list failed: login")})
	if m.screen != contexts || !strings.Contains(m.err, "login") {
		t.Fatalf("screen=%s err=%q", m.screen, m.err)
	}
}

func TestExistingContextSkipsGcloud(t *testing.T) {
	m := clusterModel()
	if cmd := m.enter(); cmd == nil || m.suspended || !m.busy {
		t.Fatal("cluster selection did not read kubeconfig first")
	}
	gke := kubeconfig.Context{Name: devCluster.ContextName(), Namespace: "default"}
	contextsResult(m, "other", gke)
	if m.screen != namespaces || m.scope.Context != gke.Name || m.suspended || !m.busy {
		t.Fatalf("existing context did not open namespaces without gcloud: screen=%s suspended=%t", m.screen, m.suspended)
	}
}

func TestMissingContextRunsGcloudThenOffersNamespaces(t *testing.T) {
	m := clusterModel()
	m.enter()
	contextsResult(m, "other", kubeconfig.Context{Name: "other", Namespace: "default"})
	if !m.suspended || m.pending == nil {
		t.Fatal("missing context did not hand the terminal to gcloud")
	}
	m.Update(result{generation: m.generation, id: m.request, kind: "gcloud get-credentials " + devCluster.Name, data: devCluster, foreground: true})
	if !m.busy || m.pending == nil || !m.pending.credsDone {
		t.Fatal("successful gcloud did not reload kubeconfig")
	}
	gke := kubeconfig.Context{Name: devCluster.ContextName(), Namespace: "default"}
	contextsResult(m, gke.Name, kubeconfig.Context{Name: "a-other", Namespace: "default"}, gke)
	if m.screen != namespaces || m.scope.Context != gke.Name {
		t.Fatalf("namespace list not opened: screen=%s scope=%+v", m.screen, m.scope)
	}
	m.Update(result{generation: m.generation, id: m.request, kind: "namespaces", data: []string{"default", "shop-dev", "shop-dev-2"}})
	if m.cursor != 1 {
		t.Fatalf("configured namespace not preselected: cursor=%d", m.cursor)
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	if m.Update(tea.KeyPressMsg{Code: tea.KeyEnter}); m.screen != pods || m.scope.Namespace != "shop-dev-2" {
		t.Fatalf("chosen namespace did not open pods: screen=%s scope=%+v", m.screen, m.scope)
	}
	if m.Update(tea.KeyPressMsg{Code: tea.KeyEscape}); m.screen != namespaces {
		t.Fatalf("Esc from pods: screen=%s", m.screen)
	}
	if m.Update(tea.KeyPressMsg{Code: tea.KeyEscape}); m.screen != clusters {
		t.Fatalf("Esc from namespaces: screen=%s", m.screen)
	}
}

func TestCtrlRForcesGcloudForExistingContext(t *testing.T) {
	m := clusterModel()
	m.Update(ctrl('r'))
	contextsResult(m, "", kubeconfig.Context{Name: devCluster.ContextName(), Namespace: "default"})
	if !m.suspended {
		t.Fatal("ctrl+r on a cluster did not run gcloud get-credentials")
	}
}

func TestNamespaceTypedByName(t *testing.T) {
	m := testModel()
	m.enterContext(kubeconfig.Context{Name: "ctx", Namespace: "default"})
	m.openNamespaces(pods)
	m.Update(result{generation: m.generation, id: m.request, kind: "namespaces", data: []string{"default"}})
	for _, r := range "team-b" {
		m.Update(key(r))
	}
	if m.Update(tea.KeyPressMsg{Code: tea.KeyEnter}); m.screen != pods || m.scope.Namespace != "team-b" {
		t.Fatalf("typed namespace not opened: screen=%s ns=%s", m.screen, m.scope.Namespace)
	}
}

func TestApplicationsGroupPodsAndOpenMenu(t *testing.T) {
	m := testModel()
	m.selectContext(kubeconfig.Context{Name: "ctx", Namespace: "ns"})
	podsResult(m, deployPod("accounts-api", "847748645f", "a1", "app"), deployPod("admin-web", "587dcb6845", "b1", "app", "proxy"),
		deployPod("admin-web", "587dcb6845", "b2", "app", "proxy"))
	if m.count() != 2 || m.cursor != 0 || m.selectedApp != "accounts-api" {
		t.Fatalf("apps not grouped: count=%d cursor=%d app=%q", m.count(), m.cursor, m.selectedApp)
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.screen != actionList || m.menuFrom != pods || m.selected.Name != "accounts-api-847748645f-a1" {
		t.Fatalf("Enter on a one-pod app did not open its action menu: screen=%s", m.screen)
	}
	if s := screenText(m); !strings.Contains(s, "Rails console") || !strings.Contains(s, "Shell") {
		t.Fatalf("menu popup not drawn:\n%s", s)
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.screen != pods || m.appFilter != "admin-web" || m.count() != 2 {
		t.Fatalf("Enter on a two-pod app did not list its pods: screen=%s filter=%q", m.screen, m.appFilter)
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.screen != actionList || m.container.Name != "app" {
		t.Fatalf("pod menu: screen=%s container=%q", m.screen, m.container.Name)
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	if m.container.Name != "proxy" {
		t.Fatalf("tab did not switch container: %q", m.container.Name)
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.appFilter != "" || m.selectedApp != "admin-web" {
		t.Fatalf("Esc did not return to applications: filter=%q app=%q", m.appFilter, m.selectedApp)
	}
}

func TestTypingFiltersFuzzilyAndEscClears(t *testing.T) {
	m := testModel()
	m.selectContext(kubeconfig.Context{Name: "ctx", Namespace: "ns"})
	podsResult(m, deployPod("accounts-api", "847748645f", "a1", "app"), deployPod("admin-web", "587dcb6845", "b1", "app"))
	for _, r := range "adweb" {
		m.Update(key(r))
	}
	if m.query != "adweb" || m.count() != 1 || m.selectedApp != "admin-web" {
		t.Fatalf("fuzzy filter: query=%q count=%d app=%q", m.query, m.count(), m.selectedApp)
	}
	if s := screenText(m); !strings.Contains(s, "⌕ adweb") {
		t.Fatalf("filter line missing:\n%s", s)
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.query != "" || m.screen != pods || m.count() != 2 {
		t.Fatal("Esc did not clear the filter first")
	}
}

func TestTargetOpensAppPodAndRunsAction(t *testing.T) {
	tg := config.Target{Name: "admin", Cluster: devCluster.Name, App: "admin-web", Action: "rails-console"}
	m := clusterModel(tg)
	if m.screen != targets {
		t.Fatalf("start screen = %s", m.screen)
	}
	m.Start(tg)
	if cmd := m.Init(); cmd == nil || !m.busy {
		t.Fatal("target did not start")
	}
	contextsResult(m, "", kubeconfig.Context{Name: devCluster.ContextName(), Namespace: "default"})
	if m.screen != pods || m.scope.Namespace != "shop-dev" {
		t.Fatalf("target scope: screen=%s scope=%+v", m.screen, m.scope)
	}
	old := deployPod("admin-web", "111", "old", "app")
	old.Status.Conditions = nil
	podsResult(m, old, deployPod("admin-web", "587dcb6845", "new", "app"), deployPod("other", "222", "x", "app"))
	if !m.suspended || m.selected == nil || m.selected.Name != "admin-web-587dcb6845-new" {
		t.Fatalf("target did not exec in the ready pod: suspended=%t selected=%v", m.suspended, m.selected)
	}
}

func TestProdAsksBeforeRunning(t *testing.T) {
	m := clusterModel()
	m.selectContext(kubeconfig.Context{Name: prodCluster.ContextName(), Namespace: "shop-prd"})
	podsResult(m, deployPod("admin-web", "587dcb6845", "b1", "app"))
	if s := screenText(m); !strings.Contains(s, "PROD") {
		t.Fatalf("no PROD badge:\n%s", s)
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m.Update(key('2'))
	if m.confirm == nil || m.suspended {
		t.Fatal("prod action ran without confirmation")
	}
	if s := screenText(m); !strings.Contains(s, "Rails console") || !strings.Contains(s, "any other key cancels") {
		t.Fatalf("confirmation not shown:\n%s", s)
	}
	m.Update(key('n'))
	if m.confirm != nil || m.suspended {
		t.Fatal("n did not cancel")
	}
	m.Update(key('2'))
	if m.Update(key('y')); !m.suspended {
		t.Fatal("y did not run the action")
	}
}

func TestLateResultFromOldContextIgnored(t *testing.T) {
	m := testModel()
	m.selectContext(kubeconfig.Context{Name: "first", Namespace: "default"})
	oldGen, oldID := m.generation, m.request
	m.selectContext(kubeconfig.Context{Name: "second", Namespace: "other"})
	m.Update(result{generation: oldGen, id: oldID, kind: "pods", data: []core.Pod{{ObjectMeta: metav1.ObjectMeta{Name: "wrong"}}}})
	if m.scope.Context != "second" || len(m.pods) != 0 {
		t.Fatalf("late data crossed scope: %+v", m.scope)
	}
}

func TestReplacementRequiresNewSelection(t *testing.T) {
	m := testModel()
	m.grouped = false
	m.selectContext(kubeconfig.Context{Name: "ctx", Namespace: "ns"})
	m.loaded = true
	m.pods = []core.Pod{{ObjectMeta: metav1.ObjectMeta{Name: "same", UID: "old"}}}
	m.selectedUID = "old"
	m.cursor = 0
	podsResult(m, core.Pod{ObjectMeta: metav1.ObjectMeta{Name: "same", UID: "new"}})
	if m.cursor != -1 || m.selectedUID != "old" {
		t.Fatalf("retargeted replacement: cursor=%d uid=%q", m.cursor, m.selectedUID)
	}
	if cmd := m.choosePod("shell"); cmd != nil || m.selected != nil {
		t.Fatal("executed or selected replacement")
	}
}

func TestExecFromMenuReturnsToPodsSelection(t *testing.T) {
	m := testModel()
	m.selectContext(kubeconfig.Context{Name: "ctx", Namespace: "ns"})
	podsResult(m, deployPod("a", "1", "x", "app"), deployPod("b", "2", "y", "app"))
	m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.Update(tea.KeyPressMsg{Code: tea.KeyEnter}); !m.suspended {
		t.Fatal("no exec command")
	}
	m.Update(result{generation: m.generation, id: m.request, kind: "exec: Shell", foreground: true})
	if m.screen != pods || m.cursor != 1 || m.selectedApp != "b" || m.sessionStatus != "Session ended" {
		t.Fatalf("lost prior screen: screen=%s cursor=%d app=%q status=%q", m.screen, m.cursor, m.selectedApp, m.sessionStatus)
	}
}

func TestReloadedContextsKeepAlwaysManual(t *testing.T) {
	m := testModel()
	c := kubeconfig.Context{Name: "gke", Namespace: "default", InteractiveMode: "Always"}
	if cmd := m.selectContext(c); cmd != nil || m.mode().Background() {
		t.Fatal("Always context read in the background before reload")
	}
	if cmd := m.loadContexts(); cmd == nil {
		t.Fatal("no reload")
	}
	plain := kubeconfig.Context{Name: "plain", Namespace: "default"}
	contextsResult(m, "gke", c, plain)
	m.View()
	if s := m.modes["gke"]; s == nil || !s.Always || s.Background() {
		t.Fatalf("reload lost Always: %+v", s)
	}
	if s := m.modes["plain"]; s == nil || s.Always || !s.Background() {
		t.Fatalf("reload did not re-evaluate an ordinary context: %+v", s)
	}
	if cmd := m.selectContext(c); cmd != nil || m.mode().Background() {
		t.Fatal("Always context read in the background after reload")
	}
	if s := screenText(m); !strings.Contains(s, "access: "+string(auth.ManualOnly)) {
		t.Fatalf("manual access not shown:\n%s", s)
	}
	// The namespace path goes through the same mode.
	if cmd := m.applyNamespace("other"); cmd == nil || !m.suspended {
		t.Fatal("namespace change in manual mode did not hand the terminal over")
	}
}

func TestErrorsShownInStatusLine(t *testing.T) {
	m := testModel()
	m.selectContext(kubeconfig.Context{Name: "ctx", Namespace: "ns"})
	m.Update(result{generation: m.generation, id: m.request, kind: "pods", err: &domain.Error{Kind: domain.Forbidden, Operation: "list pods", ExitCode: 1, Detail: "pods is forbidden"}})
	if s := screenText(m); !strings.Contains(s, "✗ Forbidden") {
		t.Fatalf("error not shown:\n%s", s)
	}
}

func TestPollDoesNotOverlap(t *testing.T) {
	m := testModel()
	m.selectContext(kubeconfig.Context{Name: "ctx", Namespace: "ns"})
	g, id := m.generation, m.request
	m.Update(poll{generation: g, token: m.pollToken})
	if m.request != id {
		t.Fatalf("started duplicate request %d vs %d", m.request, id)
	}
}
func TestNamespaceOverrideIsOnlyInitialChoice(t *testing.T) {
	m := New(context.Background(), kubectl.Client{Timeout: time.Second}, gcloud.Client{}, config.Defaults(), "", "from-flag", "")
	c := kubeconfig.Context{Name: "ctx", Namespace: "from-context"}
	m.selectContext(c)
	if m.scope.Namespace != "from-flag" {
		t.Fatal(m.scope.Namespace)
	}
	m.remembered["ctx"] = "manually-chosen"
	m.selectContext(c)
	if m.scope.Namespace != "manually-chosen" {
		t.Fatal(m.scope.Namespace)
	}
}
func TestViewNeverCreatesAccessState(t *testing.T) {
	m := testModel()
	m.scope.Context = "ghost"
	m.View()
	if _, ok := m.modes["ghost"]; ok {
		t.Fatal("View created access state")
	}
	if m.mode().Background() {
		t.Fatal("unknown context reads in the background")
	}
}
func TestForegroundLogsTakeOptionsBeforeHandoff(t *testing.T) {
	m := testModel()
	m.selectContext(kubeconfig.Context{Name: "gke", Namespace: "ns", InteractiveMode: "Always"})
	p := core.Pod{ObjectMeta: metav1.ObjectMeta{Name: "pod", UID: "uid"}}
	p.Spec.Containers = []core.Container{{Name: "app"}}
	p.Status.ContainerStatuses = []core.ContainerStatus{{Name: "app", State: core.ContainerState{Running: &core.ContainerStateRunning{}}}}
	m.pods = []core.Pod{p}
	m.selected = &m.pods[0]
	m.selectedUID = "uid"
	m.container = domain.Containers(&p)[0]
	if cmd := m.openLogs(); cmd != nil || m.screen != logs || m.suspended {
		t.Fatalf("manual mode started kubectl logs before the options were set: cmd=%v screen=%s", cmd, m.screen)
	}
	m.Update(tea.KeyPressMsg{Code: 'f', Text: "f"})
	m.Update(tea.KeyPressMsg{Code: 'p', Text: "p"})
	if !m.logOptions.Follow || !m.logOptions.Previous || m.screen != logs || m.suspended {
		t.Fatalf("options not editable before handoff: %+v screen=%s", m.logOptions, m.screen)
	}
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
	if cmd == nil || !m.suspended {
		t.Fatal("r did not hand the terminal to kubectl logs")
	}
	m.Update(result{generation: m.generation, id: m.request, kind: "foreground logs", foreground: true})
	if m.screen != logs || m.sessionStatus != "Session ended" || m.suspended || m.busy {
		t.Fatalf("did not return to the logs screen: screen=%s status=%q", m.screen, m.sessionStatus)
	}
	if _, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter}); cmd == nil || !m.suspended {
		t.Fatal("Enter did not run kubectl logs again")
	}
	m.Update(result{generation: m.generation, id: m.request, kind: "foreground logs", foreground: true, err: &domain.Error{Kind: domain.Forbidden, Operation: "logs", ExitCode: 1, Detail: "forbidden"}})
	if m.screen != logs || !strings.Contains(m.err, "Forbidden") {
		t.Fatalf("failed foreground logs left the logs screen or hid the error: screen=%s err=%q", m.screen, m.err)
	}
}
func TestInterruptedForegroundLogsIsNotAnError(t *testing.T) {
	if !interrupted(&domain.Error{Kind: domain.Unknown, ExitCode: 130}) || !interrupted(&domain.Error{Kind: domain.Unknown, ExitCode: -1, Detail: "signal: interrupt"}) {
		t.Fatal("Ctrl+C on kubectl logs reported as an error")
	}
	if interrupted(&domain.Error{Kind: domain.Forbidden, ExitCode: 1}) || interrupted(nil) {
		t.Fatal("real error treated as an interrupt")
	}
}
