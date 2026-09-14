package ui

import (
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"fmt"
	"github.com/charmbracelet/x/ansi"
	"kubernetes-terminal-browser/internal/auth"
	"kubernetes-terminal-browser/internal/config"
	"kubernetes-terminal-browser/internal/domain"
	"kubernetes-terminal-browser/internal/presentation"
	"strings"
	"time"
)

type hint struct{ key, desc string }

func (m *Model) View() tea.View {
	under := m.screen
	if under == actionList {
		under = m.menuFrom
	}
	head := m.header(under)
	foot := m.footer()
	bodyH := max(1, m.height-len(head)-len(foot))
	body := m.body(under, bodyH)
	lines := append(head, body...)
	for len(lines) < m.height-len(foot) {
		lines = append(lines, "")
	}
	s := strings.Join(append(lines, foot...), "\n")
	switch {
	case m.help:
		s = overlay(s, m.helpBox(), m.width, m.height)
	case m.screen == actionList:
		s = overlay(s, m.menuBox(), m.width, m.height)
	}
	if m.confirm != nil {
		s = overlay(s, m.confirmBox(), m.width, m.height)
	}
	v := tea.NewView(lipgloss.NewStyle().MaxWidth(m.width).MaxHeight(m.height).Render(s))
	v.AltScreen = true
	return v
}

// location is the breadcrumb of the current scope: cluster (or context),
// namespace, application, pod / container.
func (m *Model) location() string {
	var parts []string
	if m.scope.Context != "" {
		name := m.scope.Context
		if c, ok := m.cluster(); ok {
			name = c.Name
		}
		parts = append(parts, name, m.scope.Namespace)
	} else if m.pending != nil {
		parts = append(parts, m.pending.cluster.Name)
	}
	return strings.Join(parts, " › ")
}
func (m *Model) crumbs(scr screen) []string {
	switch scr {
	case targets, clusters, contexts:
		if m.pending != nil {
			return []string{m.pending.cluster.Name}
		}
		return nil
	}
	if m.scope.Context == "" {
		return nil
	}
	name := m.scope.Context
	if c, ok := m.cluster(); ok {
		name = c.Name
	}
	parts := []string{name, m.scope.Namespace}
	if scr == namespaces {
		return parts[:1]
	}
	if scr == pods && m.appFilter != "" {
		parts = append(parts, m.appFilter)
	}
	if m.selected != nil && (scr == containers || scr == logs || scr == describe || m.screen == actionList) {
		p := m.selected.Name
		if scr != describe && m.container.Name != "" {
			p += " / " + m.container.Name
		}
		parts = append(parts, p)
	}
	return parts
}
func (m *Model) screenLabel(scr screen) string {
	if scr == pods {
		if m.appMode() {
			return "Applications"
		}
		return "Pods"
	}
	return string(scr)
}
func (m *Model) header(scr screen) []string {
	title := logoStyle.Render("ktb")
	crumbs := m.crumbs(scr)
	if len(crumbs) > 0 && m.prod() && scr != targets && scr != clusters && scr != contexts {
		title += " " + prodStyle.Render(" PROD ")
	}
	for i, c := range crumbs {
		sep := dimStyle.Render(" › ")
		if i == 0 {
			sep = "  "
		}
		title += sep + titleStyle.Render(presentation.SafeText(c))
	}
	title += "  " + dimStyle.Render("· "+m.screenLabel(scr))
	var info []string
	if m.busy {
		info = append(info, accentStyle.Render(spinFrames[m.spinFrame%len(spinFrames)]+" "+m.busyLabel()))
	}
	if m.scope.Context != "" && scr != targets && scr != clusters && scr != contexts {
		if md := m.mode(); md.Mode != auth.BackgroundReady {
			info = append(info, warnStyle.Render("access: "+string(md.Mode)))
		}
	}
	if scr == pods && m.loaded {
		info = append(info, dimStyle.Render(m.podCount()))
	}
	if scr == pods && !m.lastSuccess.IsZero() {
		info = append(info, dimStyle.Render("updated "+m.lastSuccess.Format("15:04:05")))
	}
	if scr == pods && m.stale {
		info = append(info, badStyle.Render("STALE"))
	}
	lines := []string{title, strings.Join(info, dimStyle.Render("  ·  "))}
	if m.query != "" && scr != logs && scr != describe {
		lines = append(lines, accentStyle.Render("⌕ ")+presentation.SafeText(m.query)+accentStyle.Render("▏")+dimStyle.Render(fmt.Sprintf("  %d %s · esc clears", m.count(), plural(m.count(), "match", "matches"))))
	}
	return lines
}
func (m *Model) busyLabel() string {
	switch {
	case m.pending != nil:
		return "opening " + m.pending.cluster.Name
	case m.screen == pods:
		return "loading pods"
	case m.screen == namespaces:
		return "loading namespaces"
	case m.screen == contexts:
		return "reading kubeconfig"
	case m.screen == clusters:
		return "listing GKE clusters via gcloud"
	case m.screen == logs:
		return "streaming logs"
	case m.screen == describe:
		return "loading description"
	}
	return "loading"
}
func (m *Model) podCount() string {
	switch {
	case m.appMode():
		return fmt.Sprintf("%d apps · %d pods", len(presentation.Apps(m.pods)), len(m.pods))
	case m.appFilter != "":
		return fmt.Sprintf("%d pods of %s", len(m.rows()), m.appFilter)
	}
	return fmt.Sprintf("%d pods", len(m.pods))
}

// body renders the screen's content in at most h lines.
func (m *Model) body(scr screen, h int) []string {
	if scr == logs || scr == describe {
		lines := strings.Split(m.view.View(), "\n")
		if scr == logs {
			h--
		}
		if len(lines) > h {
			lines = lines[:h]
		}
		if scr == logs {
			lines = append(lines, dimStyle.Render(fmt.Sprintf("tail %d · follow %t · previous %t", m.logOptions.Tail, m.logOptions.Follow, m.logOptions.Previous)))
		}
		return lines
	}
	t := m.table(scr)
	cursor := m.cursor
	if len(t.rows) == 0 {
		return []string{"", "  " + m.emptyText(scr)}
	}
	rows := h - 1
	if len(t.rows) > rows {
		rows--
	}
	rows = max(1, rows)
	start := max(0, cursor-rows+1)
	out := strings.Split(strings.TrimRight(t.render(m.width, cursor, start, start+rows), "\n"), "\n")
	if len(t.rows) > rows {
		out = append(out, dimStyle.Render(fmt.Sprintf("  %d–%d of %d", start+1, min(len(t.rows), start+rows), len(t.rows))))
	}
	return out
}
func (m *Model) emptyText(scr screen) string {
	if m.busy {
		return accentStyle.Render(spinFrames[m.spinFrame%len(spinFrames)]) + " " + dimStyle.Render(m.busyLabel()+"…")
	}
	switch {
	case m.query != "" && scr == clusters:
		return dimStyle.Render("No matches • ctrl+f searches GCP projects starting with ") + presentation.SafeText(m.query)
	case m.query != "" && scr == namespaces:
		return dimStyle.Render("No match • Enter opens namespace ") + presentation.SafeText(m.query)
	case m.query != "":
		return dimStyle.Render("No matches • esc clears the filter")
	case scr == pods && m.loaded:
		return dimStyle.Render("0 pods; namespace existence has not been verified")
	case scr == pods:
		return dimStyle.Render("No pods loaded • ctrl+r reads")
	case scr == namespaces:
		return dimStyle.Render("Type a namespace name and press Enter • ctrl+r lists namespaces")
	case scr == clusters:
		return dimStyle.Render("No clusters • ctrl+f searches known GCP projects; type a project ID prefix first to search others")
	}
	return dimStyle.Render("Nothing here")
}

// table is the tabular form of a list screen; its rows are in the order of
// items(), apps() or rows(), so the cursor indexes both.
func (m *Model) table(scr screen) table {
	saved := m.screen
	m.screen = scr
	defer func() { m.screen = saved }()
	switch scr {
	case targets:
		t := table{headers: []string{"TARGET", "ENV", "CLUSTER", "NAMESPACE", "APP", "ACTION"}}
		for _, i := range m.items() {
			tg := m.targets[i]
			cl, _ := config.FindCluster(m.clusters, tg.Cluster)
			ns := tg.Namespace
			if ns == "" {
				ns = cl.Namespace
			}
			action := tg.Action
			if action == "" {
				action = "menu"
			}
			t.rows = append(t.rows, []cell{{tg.Name, boldStyle}, envCell(cl), {tg.Cluster, dimStyle}, {ns, dimStyle}, {tg.App, plainStyle}, {action, dimStyle}})
		}
		return t
	case clusters:
		t := table{headers: []string{"CLUSTER", "ENV", "REGION", "PROJECT", "NAMESPACE"}}
		for _, i := range m.items() {
			c := m.clusters[i]
			t.rows = append(t.rows, []cell{{c.Name, boldStyle}, envCell(c), {c.Region, dimStyle}, {c.Project, dimStyle}, {c.Namespace, dimStyle}})
		}
		return t
	case contexts:
		t := table{headers: []string{"CONTEXT", "NAMESPACE"}}
		for _, i := range m.items() {
			c := m.contexts[i]
			t.rows = append(t.rows, []cell{{c.Name, plainStyle}, {c.Namespace, dimStyle}})
		}
		return t
	case namespaces:
		t := table{headers: []string{"NAMESPACE", ""}}
		for _, i := range m.items() {
			n := m.namespaces[i]
			mark := ""
			if n == m.scope.Namespace {
				mark = "current"
			}
			t.rows = append(t.rows, []cell{{n, plainStyle}, {mark, dimStyle}})
		}
		return t
	case pods:
		if m.appMode() {
			t := table{headers: []string{"APP", "KIND", "PODS", "STATUS", "RESTARTS", "AGE"}}
			now := time.Now()
			for _, g := range m.apps() {
				t.rows = append(t.rows, groupRowCells(presentation.Group(g, now)))
			}
			return t
		}
		t := table{headers: []string{"NAME", "READY", "STATUS", "RESTARTS", "AGE"}}
		now := time.Now()
		for _, p := range m.rows() {
			t.rows = append(t.rows, podRowCells(presentation.Pod(&p, now)))
		}
		return t
	case containers:
		t := table{headers: []string{"CONTAINER", "TYPE", "STATE", "READY", "RESTARTS"}}
		if m.selected != nil {
			cs := domain.Containers(m.selected)
			for _, i := range m.items() {
				c := cs[i]
				ready, restarts := dimStyle, dimStyle
				if !c.Ready {
					ready = warnStyle
				}
				if c.Restarts > 0 {
					restarts = warnStyle
				}
				t.rows = append(t.rows, []cell{{c.Name, boldStyle}, {c.Kind, dimStyle}, {c.State, containerStateStyle(c.State, c.Ready)}, {fmt.Sprint(c.Ready), ready}, {fmt.Sprint(c.Restarts), restarts}})
			}
		}
		return t
	}
	return table{}
}
func envCell(c config.Cluster) cell {
	if c.Prod() {
		return cell{"prod", badStyle.Bold(true)}
	}
	return cell{"dev", dimStyle}
}

// footer is the status line and the key hints of the current screen.
func (m *Model) footer() []string {
	var status []string
	switch {
	case m.err != "":
		for i, l := range strings.Split(ansi.Wrap(presentation.SafeText(m.err), max(20, m.width-4), " "), "\n") {
			if i == 3 {
				break
			}
			prefix := "  "
			if i == 0 {
				prefix = "✗ "
			}
			status = append(status, badStyle.Render(prefix+l))
		}
	case m.status != "":
		status = append(status, dimStyle.Render(presentation.SafeText(m.status)))
	}
	if m.sessionStatus != "" && m.err == "" {
		status = append(status, okStyle.Render("✓ "+presentation.SafeText(m.sessionStatus)))
	}
	if len(status) == 0 {
		status = []string{""}
	}
	return append(status, renderHints(m.hints(), m.width))
}
func renderHints(hs []hint, width int) string {
	always := []hint{{"?", "help"}, {"ctrl+c", "quit"}}
	render := func(h hint) string { return keyStyle.Render(h.key) + " " + dimStyle.Render(h.desc) }
	tail := render(always[0]) + "   " + render(always[1])
	used := ansi.StringWidth(tail)
	var parts []string
	for _, h := range hs {
		s := render(h)
		if used+ansi.StringWidth(s)+3 > width {
			break
		}
		parts = append(parts, s)
		used += ansi.StringWidth(s) + 3
	}
	return strings.Join(append(parts, tail), "   ")
}
func (m *Model) hints() []hint {
	switch {
	case m.confirm != nil:
		return []hint{{"y", "run"}, {"any key", "cancel"}}
	case m.help:
		return []hint{{"esc", "close help"}}
	}
	switch m.screen {
	case targets:
		return []hint{{"enter", "open"}, {"type", "filter"}, {"tab", "clusters"}, {"ctrl+f", "find clusters"}, {"ctrl+k", "contexts"}}
	case clusters:
		h := []hint{{"enter", "open"}, {"type", "filter"}, {"ctrl+f", "find clusters"}}
		if m.query != "" {
			h[2].desc = "search projects " + presentation.SafeText(m.query) + "*"
		}
		if len(m.targets) > 0 {
			h = append(h, hint{"tab", "targets"})
		}
		return append(h, hint{"ctrl+k", "contexts"}, hint{"ctrl+r", "refresh credentials"})
	case contexts:
		return []hint{{"enter", "open"}, {"type", "filter"}, {"ctrl+r", "reload"}, {"ctrl+f", "find clusters"}, {"esc", "back"}}
	case namespaces:
		return []hint{{"enter", "open"}, {"type", "filter / name"}, {"ctrl+r", "list"}, {"esc", "back"}}
	case pods:
		h := []hint{{"enter", "open"}, {"type", "filter"}}
		if m.appMode() {
			h = append(h, hint{"tab", "pods"})
		} else if m.appFilter == "" {
			h = append(h, hint{"tab", "apps"})
		}
		h = append(h, hint{"ctrl+l", "logs"}, hint{"ctrl+d", "describe"}, hint{"ctrl+s", "shell"}, hint{"ctrl+n", "namespace"}, hint{"ctrl+r", "refresh"})
		if m.appFilter != "" {
			return append(h, hint{"esc", "apps"})
		}
		return append(h, hint{"esc", "namespaces"})
	case containers:
		return []hint{{"enter", "actions"}, {"ctrl+s", "shell"}, {"ctrl+l", "logs"}, {"ctrl+d", "describe"}, {"esc", "back"}}
	case actionList:
		h := []hint{{"enter", "run"}, {"1-9", "run #"}}
		if m.selected != nil && len(domain.Containers(m.selected)) > 1 {
			h = append(h, hint{"tab", "container"})
		}
		return append(h, hint{"esc", "close"})
	case logs:
		return []hint{{"f", "follow"}, {"p", "previous"}, {"t", "tail"}, {"r", "reconnect"}, {"↑↓ pgup pgdn", "scroll"}, {"esc", "back"}}
	case describe:
		return []hint{{"r", "refresh"}, {"↑↓ pgup pgdn", "scroll"}, {"esc", "back"}}
	}
	return nil
}
func (m *Model) menuBox() string {
	var b strings.Builder
	if m.selected != nil {
		b.WriteString(dimStyle.Render("pod        ") + boldStyle.Render(presentation.SafeText(m.selected.Name)) + "\n")
		c := boldStyle.Render(presentation.SafeText(m.container.Name))
		if n := len(domain.Containers(m.selected)); n > 1 {
			c += dimStyle.Render(fmt.Sprintf("  (tab: %d containers)", n))
		}
		if !m.container.Running {
			c += "  " + badStyle.Render(m.container.State)
		}
		b.WriteString(dimStyle.Render("container  ") + c + "\n")
	}
	b.WriteString("\n")
	for i, a := range m.cfg.Actions {
		line := fmt.Sprintf("%d  %s", i+1, presentation.SafeText(a.Label))
		if i == m.menuCursor {
			b.WriteString(menuCursorStyle.Render("› "+line) + "\n")
		} else {
			b.WriteString("  " + line + "\n")
		}
	}
	border := accent
	if m.prod() {
		border = badColor
	}
	return boxStyle.BorderForeground(border).Render(strings.TrimRight(b.String(), "\n"))
}
func (m *Model) confirmBox() string {
	pod, container := "", m.container.Name
	if m.selected != nil {
		pod = m.selected.Name
	}
	body := prodStyle.Render(" PROD ") + " " + boldStyle.Render(presentation.SafeText(m.location())) + "\n\n" +
		"Run " + boldStyle.Render(presentation.SafeText(m.confirm.Label)) + " in\n" +
		presentation.SafeText(pod+" / "+container) + "?\n\n" +
		keyStyle.Render("y") + dimStyle.Render(" run · any other key cancels")
	return boxStyle.BorderForeground(badColor).Render(body)
}
func (m *Model) helpBox() string {
	rows := [][2]string{
		{"Flow", "target or cluster → namespace → application → pod → action"},
		{"type", "filter the list (fuzzy); esc clears, backspace / ctrl+u edit"},
		{"↑ ↓ pgup pgdn home end", "move"},
		{"enter", "open; on an application with one pod, its action menu"},
		{"tab", "targets ⇄ clusters · applications ⇄ pods · menu: container"},
		{"ctrl+s / ctrl+l / ctrl+d", "shell / logs / describe for the selected pod"},
		{"ctrl+g / ctrl+k / ctrl+n", "start screen / contexts / namespace"},
		{"ctrl+r", "refresh; on clusters: run gcloud get-credentials again"},
		{"ctrl+f", "find GKE clusters in known projects; after a typed prefix, in projects starting with it"},
		{"ctrl+b", "re-enable background reads after manual access"},
		{"logs", "f follow · p previous · t tail size · r reconnect"},
		{"esc", "back · ctrl+c quits"},
		{"", ""},
		{"prod", "commands on prod clusters ask for y first"},
		{"kubectl", presentation.SafeText(m.version)},
	}
	var b strings.Builder
	b.WriteString(boldStyle.Render("Keys") + "\n\n")
	for _, r := range rows {
		b.WriteString(keyStyle.Render(fmt.Sprintf("%-26s", r[0])) + dimStyle.Render(r[1]) + "\n")
	}
	return boxStyle.BorderForeground(accent).Render(strings.TrimRight(b.String(), "\n"))
}

// overlay dims base and draws box centered over it.
func overlay(base, box string, width, height int) string {
	lines := strings.Split(base, "\n")
	for len(lines) < height {
		lines = append(lines, "")
	}
	boxLines := strings.Split(box, "\n")
	bw := 0
	for _, l := range boxLines {
		bw = max(bw, ansi.StringWidth(l))
	}
	x := max(0, (width-bw)/2)
	y := max(0, (height-len(boxLines))/2)
	for i, l := range lines {
		plain := ansi.Strip(l)
		if i < y || i >= y+len(boxLines) {
			lines[i] = shadeStyle.Render(plain)
			continue
		}
		left := ansi.Truncate(plain, x, "")
		left += strings.Repeat(" ", x-ansi.StringWidth(left))
		right := ansi.TruncateLeft(plain, x+bw, "")
		lines[i] = shadeStyle.Render(left) + boxLines[i-y] + shadeStyle.Render(right)
	}
	return strings.Join(lines, "\n")
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
