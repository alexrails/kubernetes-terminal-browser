package ui

import (
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"context"
	"errors"
	"fmt"
	core "k8s.io/api/core/v1"
	"kubernetes-terminal-browser/internal/actions"
	"kubernetes-terminal-browser/internal/auth"
	"kubernetes-terminal-browser/internal/config"
	"kubernetes-terminal-browser/internal/domain"
	"kubernetes-terminal-browser/internal/gcloud"
	"kubernetes-terminal-browser/internal/kube"
	"kubernetes-terminal-browser/internal/kube/kubectl"
	"kubernetes-terminal-browser/internal/kubeconfig"
	"kubernetes-terminal-browser/internal/presentation"
	"kubernetes-terminal-browser/internal/terminal"
	"sort"
	"strings"
	"time"
	"unicode"
)

type screen string

const (
	targets    screen = "Targets"
	clusters   screen = "Clusters"
	contexts   screen = "Contexts"
	namespaces screen = "Namespaces"
	pods       screen = "Pods"
	containers screen = "Containers"
	actionList screen = "Actions"
	logs       screen = "Logs"
	describe   screen = "Describe"
)

type result struct {
	generation, id uint64
	kind           string
	data           any
	err            error
	foreground     bool
}
type poll struct{ generation, token uint64 }
type logTick struct{ generation uint64 }
type spinTick struct{}
type detail struct {
	Body, Events string
	EventsErr    error
}

// pending is a cluster being opened: kubeconfig is checked for its context,
// gcloud get-credentials runs only when the context is missing (or on an
// explicit refresh), and a target then opens its application's pod.
type pending struct {
	cluster   config.Cluster
	target    *config.Target
	force     bool
	credsDone bool
}

type Model struct {
	root, ctx                          context.Context
	cancel                             context.CancelFunc
	client                             kubectl.Client
	gcloud                             gcloud.Client
	cfg                                config.Config
	source, namespaceOverride, version string
	scope                              domain.Scope
	generation, request, pollToken     uint64
	busy, suspended                    bool
	screen                             screen
	contexts                           []kubeconfig.Context
	clusters                           []config.Cluster
	targets                            []config.Target
	namespaces                         []string
	modes                              map[string]*auth.State
	remembered                         map[string]string
	pods                               []core.Pod
	selectedUID, selectedApp           string
	selected                           *core.Pod
	container                          domain.Container
	cursor                             int
	query, podQuery                    string
	status, err, sessionStatus         string
	resumeScreen                       screen
	resumeQuery                        string
	resumeCursor                       int
	lastSuccess                        time.Time
	stale, loaded                      bool
	width, height                      int
	help                               bool
	view                               viewport.Model
	logBuffer                          *presentation.LogBuffer
	logRevision                        uint64
	logOptions                         kube.LogsOptions
	logFollowing, logEnded             bool
	pending                            *pending
	start                              *config.Target
	// grouped shows pods by application; appFilter is the application
	// whose pods are listed after drilling into it.
	grouped   bool
	appFilter string
	// The action menu is a popup over menuFrom (pods or containers).
	menuFrom   screen
	menuCursor int
	// confirm is an action waiting for y on a prod cluster.
	confirm *config.Action
	// nsReturn is where Esc on the namespace list goes.
	nsReturn  screen
	spinFrame int
	spinning  bool
	// discover fills the cluster list from kubeconfig's GKE contexts when
	// clusters.yaml lists none; autoStart moves the first such read from
	// the contexts screen to the clusters screen.
	discover, autoStart bool
}

func New(ctx context.Context, c kubectl.Client, gc gcloud.Client, cfg config.Config, source, ns, version string) *Model {
	m := &Model{root: ctx, client: c, gcloud: gc, cfg: cfg, source: source, namespaceOverride: ns, version: version, clusters: append([]config.Cluster(nil), cfg.Clusters...), targets: append([]config.Target(nil), cfg.Targets...), width: 100, height: 30, modes: map[string]*auth.State{}, remembered: map[string]string{}, view: viewport.New(), logOptions: kube.LogsOptions{Tail: 200}, logFollowing: true, grouped: true}
	m.discover = len(m.clusters) == 0
	m.autoStart = m.discover
	m.screen = m.startScreen()
	if m.screen == contexts {
		m.status = "Loading contexts"
	}
	m.reset()
	return m
}

// Start opens target t as soon as the program runs.
func (m *Model) Start(t config.Target) { m.start = &t }

func (m *Model) startScreen() screen {
	switch {
	case len(m.targets) > 0:
		return targets
	case len(m.clusters) > 0:
		return clusters
	}
	return contexts
}
func (m *Model) showStart() tea.Cmd {
	m.reset()
	m.pending = nil
	m.screen = m.startScreen()
	m.query = ""
	m.cursor = 0
	if m.screen == contexts {
		return m.loadContexts()
	}
	return nil
}

// openCluster checks kubeconfig for the cluster's context first; gcloud runs
// only if it is missing or force is set.
func (m *Model) openCluster(cl config.Cluster, t *config.Target, force bool) tea.Cmd {
	m.reset()
	m.pending = &pending{cluster: cl, target: t, force: force}
	m.status = "Opening " + cl.Name
	return m.loadContexts()
}
func (m *Model) openTarget(t config.Target) tea.Cmd {
	cl, e := config.FindCluster(m.clusters, t.Cluster)
	if e != nil {
		m.err = e.Error()
		return nil
	}
	return m.openCluster(cl, &t, false)
}

// found is a gcloud cluster search over the known projects or, with prefix,
// over the projects whose ID starts with it.
type found struct {
	clusters []config.Cluster
	projects int
	prefix   string
}

// listClusters asks gcloud for clusters. A prefix typed on the clusters screen
// searches the projects starting with it. Otherwise only the projects already
// known from clusters.yaml, kubeconfig and gcloud configurations are scanned:
// an account may see thousands of projects, one gcloud call each.
func (m *Model) listClusters() tea.Cmd {
	prefix := ""
	if m.screen == clusters {
		prefix = m.query
	}
	if prefix != "" && !gcloud.ValidPrefix(prefix) {
		m.err = fmt.Sprintf("%q is not a project ID prefix: use lowercase letters, digits and hyphens", prefix)
		return nil
	}
	known := map[string]bool{}
	for _, c := range m.clusters {
		known[c.Project] = true
	}
	m.reset()
	m.pending = nil
	if m.screen != clusters {
		m.screen, m.query = clusters, ""
	}
	m.cursor = 0
	m.status = ""
	ctx, g, id := m.ticket()
	gc, c, src := m.gcloud, m.client, m.source
	return func() tea.Msg {
		v, e := search(ctx, gc, c, src, prefix, known)
		return result{generation: g, id: id, kind: "gcloud clusters", data: v, err: e}
	}
}
func search(ctx context.Context, gc gcloud.Client, c kubectl.Client, src, prefix string, known map[string]bool) (found, error) {
	f := found{prefix: prefix}
	var ps []string
	if prefix != "" {
		var e error
		if ps, e = gc.Projects(ctx, c.Timeout, prefix); e != nil {
			return f, e
		}
	} else {
		kctx, cancel := context.WithTimeout(ctx, c.Timeout)
		snap, e := kubeconfig.Load(kctx, c.Executable, src)
		cancel()
		if e == nil {
			for _, k := range snap.Contexts {
				if cl, ok := config.ClusterFromContext(k.Name); ok {
					known[cl.Project] = true
				}
			}
		}
		if cp, e := gc.ConfiguredProjects(ctx, c.Timeout); e == nil {
			for _, p := range cp {
				known[p] = true
			}
		}
		for p := range known {
			ps = append(ps, p)
		}
		sort.Strings(ps)
		if len(ps) == 0 {
			return f, errors.New("No known GCP projects • type a project ID prefix and press ctrl+f")
		}
	}
	f.projects = len(ps)
	cs, e := gc.Clusters(ctx, c.Timeout, ps)
	f.clusters = cs
	return f, e
}

// addClusters appends the clusters not listed yet, matched by context name,
// and reports how many were new.
func (m *Model) addClusters(cs []config.Cluster) int {
	seen := map[string]bool{}
	for _, c := range m.clusters {
		seen[c.ContextName()] = true
	}
	n := 0
	for _, c := range cs {
		if !seen[c.ContextName()] {
			seen[c.ContextName()] = true
			m.clusters = append(m.clusters, c)
			n++
		}
	}
	return n
}
func (m *Model) connectCluster(cl config.Cluster) tea.Cmd {
	m.sessionStatus = ""
	gc := m.gcloud
	return m.foreground("gcloud get-credentials "+cl.Name, func(ctx context.Context, _ kubectl.Client, t terminal.IO) (any, error) {
		return cl, gc.GetCredentials(ctx, cl, t)
	})
}
func (m *Model) reset() {
	if m.cancel != nil {
		m.cancel()
	}
	m.ctx, m.cancel = context.WithCancel(m.root)
	m.generation++
	m.scope.Generation = m.generation
	m.busy = false
	m.suspended = false
	m.pollToken++
	m.logBuffer = nil
	m.err = ""
}
func (m *Model) Init() tea.Cmd {
	if m.start != nil {
		t := *m.start
		m.start = nil
		return m.openTarget(t)
	}
	if m.screen == contexts {
		return m.loadContexts()
	}
	return nil
}
func (m *Model) ticket() (context.Context, uint64, uint64) {
	m.request++
	m.busy = true
	return m.ctx, m.generation, m.request
}
func (m *Model) loadContexts() tea.Cmd {
	if m.busy {
		return nil
	}
	ctx, g, id := m.ticket()
	c, src := m.client, m.source
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, c.Timeout)
		defer cancel()
		v, e := kubeconfig.Load(ctx, c.Executable, src)
		return result{generation: g, id: id, kind: "contexts", data: v, err: e}
	}
}

// mode reports the access state of the selected context. It never creates an
// entry: entries are made only in Update paths (enterContext, the contexts
// result and ctrl+b), so View cannot change state. An unknown context reads as
// manual-only, the safe direction for an interactive credential plugin.
func (m *Model) mode() *auth.State {
	if s := m.modes[m.scope.Context]; s != nil {
		return s
	}
	return statePtr(auth.New(true))
}

// cluster is the configured cluster of the current context, if any.
func (m *Model) cluster() (config.Cluster, bool) {
	for _, c := range m.clusters {
		if c.ContextName() == m.scope.Context {
			return c, true
		}
	}
	return config.Cluster{}, false
}

// prod reports whether the current context needs confirmation for commands.
// A context outside clusters.yaml is judged by its name.
func (m *Model) prod() bool {
	if c, ok := m.cluster(); ok {
		return c.Prod()
	}
	return strings.Contains(m.scope.Context, "-prd") || strings.Contains(m.scope.Context, "-prod")
}
func (m *Model) selectContext(c kubeconfig.Context) tea.Cmd { return m.selectContextNS(c, "") }
func (m *Model) selectContextNS(c kubeconfig.Context, ns string) tea.Cmd {
	m.enterContext(c)
	if ns != "" {
		m.scope.Namespace = ns
	}
	if !m.mode().Background() {
		m.status = "Manual access: ctrl+r reads using the terminal"
		return nil
	}
	return m.loadPods(false)
}

// enterContext makes c the current scope without reading anything.
func (m *Model) enterContext(c kubeconfig.Context) {
	m.reset()
	ns := m.remembered[c.Name]
	if ns == "" {
		ns = c.Namespace
		if m.namespaceOverride != "" {
			ns = m.namespaceOverride
		}
	}
	m.scope = domain.Scope{Kubeconfig: m.source, Context: c.Name, Namespace: ns, Generation: m.generation}
	m.clearPods()
	m.status = ""
	m.sessionStatus = ""
	if _, ok := m.modes[c.Name]; !ok {
		v := auth.New(c.InteractiveMode == "Always")
		m.modes[c.Name] = &v
	}
}
func (m *Model) clearPods() {
	m.screen = pods
	m.pods = nil
	m.selected = nil
	m.selectedUID = ""
	m.selectedApp = ""
	m.appFilter = ""
	m.query = ""
	m.podQuery = ""
	m.cursor = 0
	m.lastSuccess = time.Time{}
	m.loaded = false
	m.stale = false
}

// chooseNamespace opens the namespace list for c; the cursor lands on the
// context's initial namespace once the list arrives.
func (m *Model) chooseNamespace(c kubeconfig.Context) tea.Cmd {
	m.enterContext(c)
	return m.openNamespaces(m.startScreen())
}
func (m *Model) openNamespaces(back screen) tea.Cmd {
	m.reset()
	m.screen = namespaces
	m.nsReturn = back
	m.query = ""
	m.cursor = 0
	m.namespaces = nil
	m.status = "Type to filter or enter a namespace name"
	if !m.mode().Background() {
		m.status = "Type a namespace name • ctrl+r lists namespaces using the terminal"
		return nil
	}
	return m.loadNamespaces()
}
func (m *Model) applyNamespace(s string) tea.Cmd {
	if !config.ValidNamespace(s) {
		m.err = "Invalid namespace: use a DNS label of up to 63 characters"
		return nil
	}
	m.reset()
	m.scope.Namespace = s
	m.sessionStatus = ""
	m.status = ""
	m.remembered[m.scope.Context] = s
	m.clearPods()
	return m.loadPods(!m.mode().Background())
}
func (m *Model) schedule() tea.Cmd {
	if m.cfg.Refresh == 0 || !m.mode().Background() || m.screen != pods || m.suspended {
		return nil
	}
	m.pollToken++
	g, t, delay := m.generation, m.pollToken, m.cfg.Refresh
	return tea.Tick(delay, func(time.Time) tea.Msg { return poll{g, t} })
}
func (m *Model) foreground(kind string, fn func(context.Context, kubectl.Client, terminal.IO) (any, error)) tea.Cmd {
	return m.foregroundAt(kind, m.location(), fn)
}

// foregroundAt hands the terminal to fn under a banner naming kind and where.
func (m *Model) foregroundAt(kind, where string, fn func(context.Context, kubectl.Client, terminal.IO) (any, error)) tea.Cmd {
	m.reset()
	m.suspended = true
	ctx, g, id := m.ticket()
	c := m.client
	var data any
	var runErr error
	session := &terminal.Session{Do: func(t terminal.IO) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		fmt.Fprintf(t.Out, "\n%s %s\n%s\n\n", bannerStyle.Render(" ktb "), titleStyle.Render(presentation.SafeText(kind)),
			dimStyle.Render(presentation.SafeText(where)+" · ktb returns when the command exits"))
		c.Foreground = &t
		data, runErr = fn(ctx, c, t)
		return runErr
	}}
	return tea.Exec(session, func(e error) tea.Msg {
		if e == nil {
			e = runErr
		}
		return result{generation: g, id: id, kind: kind, data: data, err: e, foreground: true}
	})
}
func (m *Model) loadPods(manual bool) tea.Cmd {
	if m.busy {
		return nil
	}
	s := m.scope
	if manual {
		m.mode().BeginForeground()
		return m.foreground("pods", func(ctx context.Context, c kubectl.Client, _ terminal.IO) (any, error) { return c.Pods(ctx, s) })
	}
	if !m.mode().Background() {
		return nil
	}
	ctx, g, id := m.ticket()
	c := m.client
	return func() tea.Msg {
		v, e := c.Pods(ctx, s)
		return result{generation: g, id: id, kind: "pods", data: v, err: e}
	}
}
func (m *Model) loadNamespaces() tea.Cmd {
	if m.busy {
		return nil
	}
	s := m.scope
	if !m.mode().Background() {
		return m.foreground("namespaces", func(ctx context.Context, c kubectl.Client, _ terminal.IO) (any, error) { return c.Namespaces(ctx, s) })
	}
	ctx, g, id := m.ticket()
	c := m.client
	return func() tea.Msg {
		v, e := c.Namespaces(ctx, s)
		return result{generation: g, id: id, kind: "namespaces", data: v, err: e}
	}
}

// rank returns the indices of labels matching query, best match first; an
// empty query keeps every label in its order.
func rank(labels []string, query string) []int {
	type hit struct{ i, score int }
	var hits []hit
	for i, l := range labels {
		if s, ok := presentation.Fuzzy(l, query); ok {
			hits = append(hits, hit{i, s})
		}
	}
	if query != "" {
		sort.SliceStable(hits, func(a, b int) bool { return hits[a].score > hits[b].score })
	}
	out := make([]int, len(hits))
	for i, h := range hits {
		out[i] = h.i
	}
	return out
}

// appMode reports whether the pods screen lists applications.
func (m *Model) appMode() bool { return m.grouped && m.appFilter == "" }
func (m *Model) apps() []presentation.AppGroup {
	all := presentation.Apps(m.pods)
	labels := make([]string, len(all))
	for i, g := range all {
		labels[i] = g.Name
	}
	var out []presentation.AppGroup
	for _, i := range rank(labels, m.query) {
		out = append(out, all[i])
	}
	return out
}

// rows are the pods listed: those of appFilter when set, matching the query.
func (m *Model) rows() []core.Pod {
	var all []core.Pod
	for _, p := range m.pods {
		if name, _ := presentation.App(&p); m.appFilter == "" || name == m.appFilter {
			all = append(all, p)
		}
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].Name < all[j].Name })
	labels := make([]string, len(all))
	for i, p := range all {
		labels[i] = p.Name
	}
	var out []core.Pod
	for _, i := range rank(labels, m.query) {
		out = append(out, all[i])
	}
	return out
}
func (m *Model) podRef() domain.PodRef {
	return domain.PodRef{Scope: m.scope, Name: m.selected.Name, UID: string(m.selected.UID)}
}
func (m *Model) containerRef() domain.ContainerRef {
	return domain.ContainerRef{Pod: m.podRef(), Name: m.container.Name, Kind: m.container.Kind}
}

// choosePod acts on the pod under the cursor. On an application, Enter opens
// its only pod or lists its pods; the other actions use its best pod.
func (m *Model) choosePod(next string) tea.Cmd {
	if m.appMode() {
		apps := m.apps()
		if m.cursor < 0 || m.cursor >= len(apps) {
			m.status = "Select an application with the arrow keys first"
			return nil
		}
		g := apps[m.cursor]
		choices := activePods(g.Pods)
		if len(choices) == 0 {
			choices = g.Pods
		}
		if next == "" && len(choices) > 1 {
			if p, ok := presentation.BestPod(choices); ok {
				m.selectedUID = string(p.UID)
			}
			m.appFilter = g.Name
			m.setQuery("")
			return nil
		}
		p, ok := presentation.BestPod(choices)
		if !ok {
			return nil
		}
		m.selectedUID = string(p.UID)
		return m.openPod(p, next)
	}
	rows := m.rows()
	if m.cursor < 0 || m.cursor >= len(rows) || m.selectedUID == "" {
		m.status = "Select a pod with the arrow keys first"
		return nil
	}
	p := rows[m.cursor]
	if string(p.UID) != m.selectedUID {
		return nil
	}
	return m.openPod(p, next)
}
func activePods(ps []core.Pod) []core.Pod {
	var out []core.Pod
	for _, p := range ps {
		if presentation.Active(&p) {
			out = append(out, p)
		}
	}
	return out
}
func (m *Model) openPod(p core.Pod, next string) tea.Cmd {
	if m.selected == nil || m.selected.UID != p.UID {
		m.sessionStatus = ""
	}
	m.selected = &p
	m.podQuery = m.query
	if next == "describe" {
		return m.openDescribe()
	}
	cs := domain.Containers(&p)
	if len(cs) == 0 {
		m.status = "No containers"
		return nil
	}
	m.container = cs[defaultContainer(&p, cs)]
	if next == "" {
		// Enter on a pod: the action menu (shell, Rails console…) for the
		// container kubectl exec would pick without -c.
		m.openMenu(pods)
		return nil
	}
	if len(cs) == 1 {
		switch next {
		case "shell":
			return m.execute(actions.Shell(m.cfg))
		case "logs":
			return m.openLogs()
		}
	}
	m.reset()
	m.screen = containers
	m.query = ""
	m.cursor = defaultContainer(&p, cs)
	return nil
}
func defaultContainer(p *core.Pod, cs []domain.Container) int {
	def := p.Annotations["kubectl.kubernetes.io/default-container"]
	for i, c := range cs {
		if c.Name == def {
			return i
		}
	}
	return 0
}
func (m *Model) openMenu(from screen) {
	m.screen = actionList
	m.menuFrom = from
	m.menuCursor = 0
}

// cycleContainer switches the action menu to the next container of the pod.
func (m *Model) cycleContainer() {
	if m.selected == nil {
		return
	}
	cs := domain.Containers(m.selected)
	for i, c := range cs {
		if c.Name == m.container.Name {
			m.container = cs[(i+1)%len(cs)]
			return
		}
	}
}

// execute runs a on the chosen container; on a prod cluster it first asks.
func (m *Model) execute(a config.Action) tea.Cmd {
	if !m.container.Running {
		m.err = "Container " + m.container.Name + " is not running; logs may still be available"
		return nil
	}
	if m.prod() {
		m.confirm = &a
		return nil
	}
	return m.run(a)
}
func (m *Model) run(a config.Action) tea.Cmd {
	r := m.containerRef()
	m.resumeScreen, m.resumeQuery, m.resumeCursor = m.screen, m.query, m.cursor
	if m.screen == actionList {
		m.resumeScreen = m.menuFrom
	}
	m.sessionStatus = ""
	m.status = ""
	where := m.location() + " › " + r.Pod.Name + " / " + r.Name
	return m.foregroundAt("exec: "+a.Label, where, func(ctx context.Context, c kubectl.Client, t terminal.IO) (any, error) {
		return nil, c.Exec(ctx, r, a, t)
	})
}
func (m *Model) restoreSession() {
	m.reset()
	m.screen = m.resumeScreen
	m.query = m.resumeQuery
	m.cursor = m.resumeCursor
	if m.screen == pods {
		m.restoreSelection(false)
	}
}
func (m *Model) openDescribe() tea.Cmd {
	m.reset()
	m.screen = describe
	m.view.SetContent("Loading description…")
	r := m.podRef()
	read := func(ctx context.Context, c kubectl.Client) (any, error) {
		body, e := c.Describe(ctx, r)
		if e != nil {
			return nil, e
		}
		ev, ee := c.Events(ctx, r)
		return detail{body, ev, ee}, nil
	}
	if !m.mode().Background() {
		return m.foreground("describe", func(ctx context.Context, c kubectl.Client, _ terminal.IO) (any, error) { return read(ctx, c) })
	}
	ctx, g, id := m.ticket()
	c := m.client
	return func() tea.Msg {
		v, e := read(ctx, c)
		return result{generation: g, id: id, kind: "describe", data: v, err: e}
	}
}
func (m *Model) openLogs() tea.Cmd {
	m.reset()
	m.screen = logs
	m.logFollowing = true
	m.logEnded = false
	m.logRevision = 0
	m.view.SetContent("Loading logs…")
	if !m.mode().Background() {
		// Manual access: no subprocess yet. The options are chosen on this
		// screen and r or Enter hands the terminal over (runForegroundLogs),
		// so tail, follow and previous work in this mode too.
		m.logEnded = true
		m.view.SetContent(foregroundLogsHelp)
		m.status = "r / Enter: run kubectl logs in the terminal"
		return nil
	}
	r, o := m.containerRef(), m.logOptions
	ctx, g, id := m.ticket()
	buf := presentation.NewLogBuffer()
	m.logBuffer = buf
	c := m.client
	return tea.Batch(func() tea.Msg {
		e := c.Logs(ctx, r, o, buf)
		return result{generation: g, id: id, kind: "logs", err: e}
	}, tea.Tick(100*time.Millisecond, func(time.Time) tea.Msg { return logTick{g} }))
}

const foregroundLogsHelp = "Manual logs: kubectl logs runs in the terminal with the options below.\nf / p / t change follow, previous and tail. r or Enter runs it.\nAfter kubectl exits (Ctrl+C stops follow), press Enter there to return here.\nScrolling is not available in this mode."

// runForegroundLogs hands the terminal to kubectl logs and keeps it there
// until Enter, so the output can be read before the TUI redraws. A stream the
// user stopped with Ctrl+C is a normal end, not an error.
func (m *Model) runForegroundLogs() tea.Cmd {
	r, o := m.containerRef(), m.logOptions
	m.status = ""
	m.sessionStatus = ""
	return m.foreground("foreground logs", func(ctx context.Context, c kubectl.Client, t terminal.IO) (any, error) {
		e := c.Logs(ctx, r, o, nil)
		if interrupted(e) {
			e = nil
		}
		terminal.WaitForEnter(ctx, t, "\nktb: kubectl logs finished. Press Enter to return.\n")
		return nil, e
	})
}
func interrupted(e error) bool {
	var v *domain.Error
	return errors.As(e, &v) && (v.ExitCode == 130 || strings.Contains(v.Detail, "signal: interrupt"))
}
func (m *Model) back() tea.Cmd {
	switch m.screen {
	case actionList:
		m.screen = m.menuFrom
		if m.screen == pods {
			return m.loadPods(false)
		}
		return nil
	case namespaces:
		if m.nsReturn == pods && m.scope.Context != "" {
			break
		}
		if m.startScreen() != contexts {
			return m.showStart()
		}
		if m.scope.Context == "" {
			return nil
		}
	case pods:
		if m.appFilter != "" {
			m.selectedApp = m.appFilter
			m.appFilter = ""
			m.setQuery("")
			return nil
		}
		if m.startScreen() != contexts {
			return m.openNamespaces(m.startScreen())
		}
		m.reset()
		m.screen = contexts
		m.query = ""
		m.cursor = 0
		return m.loadContexts()
	case contexts:
		if m.startScreen() != contexts {
			return m.showStart()
		}
		return nil
	case clusters:
		if len(m.targets) > 0 {
			return m.showStart()
		}
		return nil
	case targets:
		return nil
	case logs:
		if m.selected != nil && len(domain.Containers(m.selected)) > 1 {
			m.reset()
			m.screen = containers
			m.query = ""
			m.cursor = 0
			return nil
		}
	}
	m.reset()
	m.screen = pods
	m.query = m.podQuery
	m.restoreSelection(false)
	return m.loadPods(false)
}

// restoreSelection puts the cursor back on the selected application or pod.
// A pod replaced under the same name is not retargeted: it must be selected
// again, so no command reaches a pod the user did not choose.
func (m *Model) restoreSelection(first bool) {
	if m.appMode() {
		apps := m.apps()
		m.cursor = -1
		for i, g := range apps {
			if g.Name == m.selectedApp {
				m.cursor = i
				return
			}
		}
		if len(apps) > 0 {
			m.cursor = 0
			m.selectedApp = apps[0].Name
		}
		return
	}
	rows := m.rows()
	m.cursor = -1
	for i, p := range rows {
		if string(p.UID) == m.selectedUID {
			m.cursor = i
			return
		}
	}
	if first && len(rows) > 0 {
		m.cursor = 0
		m.selectedUID = string(rows[0].UID)
	} else if m.selectedUID != "" {
		m.status = "Selected pod is absent or hidden; select a pod again"
	}
}

// setQuery changes the filter: a new query selects its best match, clearing
// it keeps the selection.
func (m *Model) setQuery(q string) {
	m.query = q
	m.cursor = 0
	if m.screen != pods {
		return
	}
	m.podQuery = q
	if q == "" {
		m.restoreSelection(false)
		return
	}
	if m.appMode() {
		if apps := m.apps(); len(apps) > 0 {
			m.selectedApp = apps[0].Name
		}
	} else if rows := m.rows(); len(rows) > 0 {
		m.selectedUID = string(rows[0].UID)
	} else {
		m.cursor = -1
	}
}
func credentials(e error) bool {
	var v *domain.Error
	return errors.As(e, &v) && v.Kind == domain.Unauthorized
}
func findContext(cs []kubeconfig.Context, name string) (kubeconfig.Context, bool) {
	for _, c := range cs {
		if c.Name == name {
			return c, true
		}
	}
	return kubeconfig.Context{}, false
}

var spinFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

func spin() tea.Cmd {
	return tea.Tick(90*time.Millisecond, func(time.Time) tea.Msg { return spinTick{} })
}

// Update animates the spinner while a request runs, around update.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if _, ok := msg.(spinTick); ok {
		m.spinFrame++
		if !m.busy {
			m.spinning = false
			return m, nil
		}
		return m, spin()
	}
	model, cmd := m.update(msg)
	if m.busy && !m.spinning {
		m.spinning = true
		cmd = tea.Batch(cmd, spin())
	}
	return model, cmd
}
func (m *Model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.root.Err() != nil {
		return m, tea.Quit
	}
	switch v := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = v.Width
		m.height = v.Height
		m.view.SetWidth(max(10, m.width-2))
		m.view.SetHeight(max(1, m.height-6))
		return m, nil
	case poll:
		if v.generation == m.generation && v.token == m.pollToken && m.screen == pods && !m.busy {
			return m, m.loadPods(false)
		}
		return m, nil
	case logTick:
		if v.generation != m.generation || m.logBuffer == nil {
			return m, nil
		}
		m.syncLogs()
		if m.logEnded {
			return m, nil
		}
		g := m.generation
		return m, tea.Tick(100*time.Millisecond, func(time.Time) tea.Msg { return logTick{g} })
	case result:
		return m, m.handleResult(v)
	case tea.KeyPressMsg:
		return m, m.key(v)
	}
	return m, nil
}
func (m *Model) handleResult(v result) tea.Cmd {
	if v.generation != m.generation || v.id != m.request {
		return nil
	}
	m.busy = false
	m.suspended = false
	if v.kind == "pods" && v.foreground {
		m.mode().EndForeground(v.err == nil || !credentials(v.err))
	} else if v.err != nil {
		m.mode().Failure(credentials(v.err))
	}
	if v.err != nil {
		m.pending = nil
		m.status = ""
		if strings.HasPrefix(v.kind, "exec:") {
			m.restoreSession()
			var ke *domain.Error
			if errors.As(v.err, &ke) && (ke.Kind == domain.Replaced || ke.Kind == domain.NotFound) {
				m.selectedUID = ""
				m.selected = nil
				m.screen = pods
				m.query = m.podQuery
				m.cursor = -1
			}
			m.err = v.err.Error()
			return m.loadPods(false)
		}
		m.err = v.err.Error()
		switch v.kind {
		case "gcloud clusters":
			if len(m.clusters) == 0 {
				m.screen = contexts
			}
		case "pods":
			m.stale = true
		case "logs":
			m.logEnded = true
			m.syncLogs()
		case "describe":
			m.view.SetContent(presentation.SafeText(m.err))
		}
		var ke *domain.Error
		if errors.As(v.err, &ke) && (ke.Kind == domain.Replaced || ke.Kind == domain.NotFound) {
			m.selectedUID = ""
		}
		return nil
	}
	m.err = ""
	switch {
	case v.kind == "contexts":
		snap := v.data.(kubeconfig.Snapshot)
		m.contexts = snap.Contexts
		// A reloaded kubeconfig re-evaluates every context's mode from its
		// interactiveMode: an Always plugin never reads in the background,
		// whatever the context did before the reload.
		m.modes = map[string]*auth.State{}
		for _, c := range m.contexts {
			m.modes[c.Name] = statePtr(auth.New(c.InteractiveMode == "Always"))
		}
		m.status = ""
		if m.discover {
			var cs []config.Cluster
			for _, c := range m.contexts {
				if cl, ok := config.ClusterFromContext(c.Name); ok {
					cs = append(cs, cl)
				}
			}
			sort.Slice(cs, func(i, j int) bool { return cs[i].Name < cs[j].Name })
			m.addClusters(cs)
		}
		if m.autoStart {
			m.autoStart = false
			if m.screen == contexts && m.pending == nil && len(m.clusters) > 0 {
				m.screen, m.query, m.cursor = clusters, "", 0
			}
		}
		if p := m.pending; p != nil {
			return m.continueCluster(p, snap)
		}
	case v.kind == "gcloud clusters":
		f := v.data.(found)
		n := m.addClusters(f.clusters)
		if f.prefix == "" {
			m.status = fmt.Sprintf("Found %d clusters in %d known projects, %d new • to search other projects, type a project ID prefix and press ctrl+f", len(f.clusters), f.projects, n)
		} else {
			m.status = fmt.Sprintf("Found %d clusters in %d projects %s*, %d new", len(f.clusters), f.projects, f.prefix, n)
		}
	case strings.HasPrefix(v.kind, "gcloud get-credentials"):
		if m.pending != nil {
			m.pending.credsDone = true
		}
		m.status = "Credentials updated; reading kubeconfig"
		return m.loadContexts()
	case v.kind == "pods":
		first := !m.loaded
		m.pods = v.data.([]core.Pod)
		m.loaded = true
		m.stale = false
		m.lastSuccess = time.Now()
		m.status = ""
		if m.screen == pods {
			m.restoreSelection(first)
		} else if m.selected != nil {
			found := false
			for i := range m.pods {
				if string(m.pods[i].UID) == m.selectedUID {
					m.selected = &m.pods[i]
					found = true
					break
				}
			}
			if !found {
				m.selected = nil
				m.selectedUID = ""
				m.status = "Selected pod disappeared; select again"
				m.screen = pods
				m.query = m.podQuery
				m.cursor = -1
			}
		}
		if !v.foreground {
			m.mode().BackgroundSuccess()
		}
		var target tea.Cmd
		if p := m.pending; p != nil && p.target != nil && m.screen == pods {
			m.pending = nil
			target = m.openTargetPod(*p.target)
		}
		if v.foreground && m.mode().Background() {
			return tea.Batch(target, m.loadPods(false))
		}
		return tea.Batch(target, m.schedule())
	case v.kind == "namespaces":
		m.namespaces = v.data.([]string)
		m.status = ""
		m.cursor = 0
		for i, n := range m.namespaces {
			if n == m.scope.Namespace {
				m.cursor = i
			}
		}
	case v.kind == "describe":
		d := v.data.(detail)
		body := d.Body + "\nEvents (selected pod UID):\n"
		if d.EventsErr != nil {
			body += d.EventsErr.Error()
		} else {
			body += d.Events
		}
		m.view.SetContent(presentation.SafeText(body))
		m.view.GotoTop()
		m.status = ""
	case v.kind == "logs":
		m.logEnded = true
		m.syncLogs()
		m.status = "Log stream ended • r: reconnect"
	case strings.HasPrefix(v.kind, "exec:"):
		m.restoreSession()
		m.sessionStatus = "Session ended"
		return m.loadPods(false)
	case v.kind == "foreground logs":
		// Stay on the logs screen: the options can be changed and the
		// stream run again; Esc goes back.
		m.sessionStatus = "Session ended"
		m.status = "r / Enter: run again • f / p / t: options • Esc: back"
	}
	return nil
}

// continueCluster runs after kubeconfig is read for a pending cluster: an
// existing context opens directly, a missing one runs gcloud first.
func (m *Model) continueCluster(p *pending, snap kubeconfig.Snapshot) tea.Cmd {
	c, ok := findContext(m.contexts, p.cluster.ContextName())
	if !ok && p.credsDone {
		c, ok = findContext(m.contexts, snap.Current)
	}
	if !ok || (p.force && !p.credsDone) {
		if p.credsDone {
			m.pending = nil
			m.screen = contexts
			m.cursor = 0
			m.err = fmt.Sprintf("Credentials updated for %s, but its context is not in this kubeconfig • select a context", p.cluster.Name)
			return nil
		}
		return m.connectCluster(p.cluster)
	}
	ns := p.cluster.Namespace
	if p.target == nil {
		m.pending = nil
		if ns != "" && m.remembered[c.Name] == "" {
			m.remembered[c.Name] = ns
		}
		return m.chooseNamespace(c)
	}
	if p.target.Namespace != "" {
		ns = p.target.Namespace
	}
	return m.selectContextNS(c, ns)
}

// openTargetPod opens the target application's best pod: its action runs at
// once, otherwise the action menu opens.
func (m *Model) openTargetPod(t config.Target) tea.Cmd {
	m.grouped = true
	m.appFilter = ""
	m.setQuery("")
	var group *presentation.AppGroup
	for _, g := range presentation.Apps(m.pods) {
		if g.Name == t.App {
			group = &g
			break
		}
	}
	if group == nil {
		m.err = fmt.Sprintf("Target %s: no pods of application %q in %s", t.Name, t.App, m.scope.Namespace)
		return nil
	}
	m.selectedApp = group.Name
	m.restoreSelection(false)
	p, ok := presentation.BestPod(activePods(group.Pods))
	if !ok {
		m.err = fmt.Sprintf("Target %s: application %q has no running pod", t.Name, t.App)
		return nil
	}
	m.selectedUID = string(p.UID)
	cmd := m.openPod(p, "")
	if m.screen != actionList {
		return cmd
	}
	if t.Container != "" {
		found := false
		for _, c := range domain.Containers(&p) {
			if c.Name == t.Container {
				m.container, found = c, true
			}
		}
		if !found {
			m.err = fmt.Sprintf("Target %s: pod %s has no container %q", t.Name, p.Name, t.Container)
			return nil
		}
	}
	for i, a := range m.cfg.Actions {
		if a.ID == t.Action {
			m.menuCursor = i
			return m.execute(a)
		}
	}
	return nil
}
func (m *Model) key(k tea.KeyPressMsg) tea.Cmd {
	key := k.String()
	if key == "ctrl+c" {
		m.cancel()
		return tea.Quit
	}
	if m.confirm != nil {
		a := *m.confirm
		m.confirm = nil
		if key == "y" || key == "Y" {
			return m.run(a)
		}
		m.status = "Cancelled"
		return nil
	}
	if m.help {
		if key == "?" || key == "esc" || key == "q" {
			m.help = false
		}
		return nil
	}
	if key == "?" {
		m.help = true
		return nil
	}
	switch m.screen {
	case logs, describe:
		return m.viewerKey(k)
	case actionList:
		return m.menuKey(k)
	}
	page := max(1, m.height-8)
	switch key {
	case "esc":
		if m.query != "" {
			m.setQuery("")
			return nil
		}
		return m.back()
	case "up", "ctrl+p":
		m.move(-1)
	case "down":
		m.move(1)
	case "pgup":
		m.move(-page)
	case "pgdown":
		m.move(page)
	case "home":
		m.move(-m.count())
	case "end":
		m.move(m.count())
	case "enter":
		return m.enter()
	case "tab":
		return m.toggle()
	case "backspace":
		if r := []rune(m.query); len(r) > 0 {
			m.setQuery(string(r[:len(r)-1]))
		}
	case "ctrl+u":
		m.setQuery("")
	case "ctrl+g":
		return m.showStart()
	case "ctrl+k":
		m.reset()
		m.pending = nil
		m.screen = contexts
		m.query = ""
		m.cursor = 0
		return m.loadContexts()
	case "ctrl+n":
		if m.scope.Context != "" {
			return m.openNamespaces(pods)
		}
	case "ctrl+r":
		return m.refresh()
	case "ctrl+f":
		if m.screen == targets || m.screen == clusters || m.screen == contexts {
			return m.listClusters()
		}
	case "ctrl+b":
		if m.screen == pods {
			if m.mode().Always {
				m.err = "Always credential plugin requires manual access"
				return nil
			}
			m.modes[m.scope.Context] = statePtr(auth.New(false))
			return m.loadPods(false)
		}
	case "ctrl+s", "ctrl+l", "ctrl+d":
		next := map[string]string{"ctrl+s": "shell", "ctrl+l": "logs", "ctrl+d": "describe"}[key]
		if m.screen == pods {
			return m.choosePod(next)
		}
		if m.screen == containers && m.pickContainer() {
			switch next {
			case "shell":
				return m.execute(actions.Shell(m.cfg))
			case "logs":
				return m.openLogs()
			default:
				return m.openDescribe()
			}
		}
	default:
		if t := k.Text; t != "" && t != " " && len([]rune(m.query)) < 128 && printable(t) {
			m.setQuery(m.query + t)
		}
	}
	return nil
}
func printable(s string) bool {
	for _, r := range s {
		if !unicode.IsPrint(r) {
			return false
		}
	}
	return true
}
func (m *Model) refresh() tea.Cmd {
	switch m.screen {
	case clusters:
		if i := m.items(); m.cursor >= 0 && m.cursor < len(i) {
			return m.openCluster(m.clusters[i[m.cursor]], nil, true)
		}
	case contexts:
		return m.loadContexts()
	case namespaces:
		return m.loadNamespaces()
	case pods:
		return m.loadPods(!m.mode().Background())
	}
	return nil
}

// toggle switches targets/clusters on the start screens and applications/pods
// on the pods screen.
func (m *Model) toggle() tea.Cmd {
	switch m.screen {
	case targets:
		m.screen = clusters
	case clusters:
		if len(m.targets) == 0 {
			return nil
		}
		m.screen = targets
	case pods:
		if m.appMode() {
			if apps := m.apps(); m.cursor >= 0 && m.cursor < len(apps) {
				if p, ok := presentation.BestPod(apps[m.cursor].Pods); ok {
					m.selectedUID = string(p.UID)
				}
			}
			m.grouped = false
		} else {
			if rows := m.rows(); m.cursor >= 0 && m.cursor < len(rows) {
				m.selectedApp, _ = presentation.App(&rows[m.cursor])
			}
			m.grouped = true
			m.appFilter = ""
		}
		m.query = ""
		m.podQuery = ""
		m.restoreSelection(false)
		if m.cursor < 0 {
			m.restoreSelection(true)
		}
		return nil
	default:
		return nil
	}
	m.query = ""
	m.cursor = 0
	return nil
}
func (m *Model) menuKey(k tea.KeyPressMsg) tea.Cmd {
	n := len(m.cfg.Actions)
	switch key := k.String(); key {
	case "esc", "q":
		return m.back()
	case "up", "k", "ctrl+p":
		m.menuCursor = max(0, m.menuCursor-1)
	case "down", "j", "ctrl+n":
		m.menuCursor = min(n-1, m.menuCursor+1)
	case "tab":
		m.cycleContainer()
	case "enter":
		if m.menuCursor >= 0 && m.menuCursor < n {
			return m.execute(m.cfg.Actions[m.menuCursor])
		}
	default:
		if len(key) == 1 && key[0] >= '1' && key[0] <= '9' && int(key[0]-'1') < n {
			m.menuCursor = int(key[0] - '1')
			return m.execute(m.cfg.Actions[m.menuCursor])
		}
	}
	return nil
}
func (m *Model) viewerKey(k tea.KeyPressMsg) tea.Cmd {
	switch k.String() {
	case "esc", "q":
		return m.back()
	case "r", "ctrl+r":
		if m.screen == logs {
			if !m.mode().Background() {
				return m.runForegroundLogs()
			}
			return m.openLogs()
		}
		return m.openDescribe()
	case "enter":
		if m.screen == logs && !m.mode().Background() {
			return m.runForegroundLogs()
		}
	case "f":
		if m.screen == logs {
			m.logOptions.Follow = !m.logOptions.Follow
			return m.openLogs()
		}
	case "p":
		if m.screen == logs {
			m.logOptions.Previous = !m.logOptions.Previous
			return m.openLogs()
		}
	case "t":
		if m.screen == logs {
			switch m.logOptions.Tail {
			case 50:
				m.logOptions.Tail = 200
			case 200:
				m.logOptions.Tail = 1000
			default:
				m.logOptions.Tail = 50
			}
			return m.openLogs()
		}
	}
	var cmd tea.Cmd
	m.view, cmd = m.view.Update(k)
	if m.screen == logs {
		m.logFollowing = m.view.AtBottom()
	}
	return cmd
}
func statePtr(s auth.State) *auth.State { return &s }
func (m *Model) syncLogs() {
	if m.logBuffer == nil {
		return
	}
	s, r, truncated := m.logBuffer.Snapshot(m.logRevision)
	if r == m.logRevision {
		return
	}
	m.logRevision = r
	if truncated {
		s = "[Older output truncated / long lines split]\n" + s
	}
	m.view.SetContent(s)
	if m.logFollowing {
		m.view.GotoBottom()
	}
}

// items are the indices, into the screen's source list, of the entries shown
// on the targets, clusters, contexts, namespaces and containers screens.
func (m *Model) items() []int {
	var labels []string
	switch m.screen {
	case targets:
		for _, t := range m.targets {
			labels = append(labels, t.Name+" "+t.App+" "+t.Cluster)
		}
	case clusters:
		for _, c := range m.clusters {
			labels = append(labels, c.Name+" "+c.Project)
		}
	case contexts:
		for _, c := range m.contexts {
			labels = append(labels, c.Name)
		}
	case namespaces:
		labels = m.namespaces
	case containers:
		if m.selected != nil {
			for _, c := range domain.Containers(m.selected) {
				labels = append(labels, c.Name)
			}
		}
	}
	return rank(labels, m.query)
}
func (m *Model) count() int {
	switch {
	case m.screen == pods && m.appMode():
		return len(m.apps())
	case m.screen == pods:
		return len(m.rows())
	}
	return len(m.items())
}
func (m *Model) move(delta int) {
	n := m.count()
	if n == 0 {
		m.cursor = -1
		return
	}
	m.cursor = max(0, min(n-1, m.cursor+delta))
	if m.screen != pods {
		return
	}
	if m.appMode() {
		m.selectedApp = m.apps()[m.cursor].Name
	} else {
		m.selectedUID = string(m.rows()[m.cursor].UID)
	}
}
func (m *Model) pickContainer() bool {
	items := m.items()
	if m.cursor < 0 || m.cursor >= len(items) || m.selected == nil {
		return false
	}
	m.container = domain.Containers(m.selected)[items[m.cursor]]
	return true
}
func (m *Model) enter() tea.Cmd {
	if m.screen == pods {
		return m.choosePod("")
	}
	items := m.items()
	if m.cursor < 0 || m.cursor >= len(items) {
		if m.screen == namespaces && m.query != "" {
			return m.applyNamespace(m.query)
		}
		return nil
	}
	i := items[m.cursor]
	switch m.screen {
	case targets:
		return m.openTarget(m.targets[i])
	case clusters:
		return m.openCluster(m.clusters[i], nil, false)
	case contexts:
		return m.selectContext(m.contexts[i])
	case namespaces:
		return m.applyNamespace(m.namespaces[i])
	case containers:
		if m.pickContainer() {
			m.openMenu(containers)
		}
	}
	return nil
}
