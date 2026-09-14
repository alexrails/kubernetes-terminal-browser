package ui

import (
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"kubernetes-terminal-browser/internal/presentation"
	"strings"
)

// Palette; 256-colour codes read on dark and light backgrounds.
var (
	accent          = lipgloss.Color("69")
	badColor        = lipgloss.Color("203")
	headerStyle     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("245"))
	plainStyle      = lipgloss.NewStyle()
	boldStyle       = lipgloss.NewStyle().Bold(true)
	dimStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	shadeStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	okStyle         = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	warnStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	badStyle        = lipgloss.NewStyle().Foreground(badColor)
	accentStyle     = lipgloss.NewStyle().Foreground(accent)
	keyStyle        = lipgloss.NewStyle().Bold(true).Foreground(accent)
	logoStyle       = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("231")).Background(accent).Padding(0, 1)
	bannerStyle     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("231")).Background(accent)
	titleStyle      = lipgloss.NewStyle().Bold(true)
	prodStyle       = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("231")).Background(badColor)
	cursorStyle     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("231")).Background(lipgloss.Color("61"))
	menuCursorStyle = lipgloss.NewStyle().Bold(true).Foreground(accent)
	boxStyle        = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 2)
)

const columnGap = "   "

type cell struct {
	text  string
	style lipgloss.Style
}
type table struct {
	headers []string
	rows    [][]cell
	// shrink is the column that gives up width (truncated with …) when the
	// table is wider than the terminal; the others keep their full width.
	shrink int
}

// widths sizes columns over every row, not only the visible ones, so the
// layout does not jump while scrolling.
func (t table) widths(total int) []int {
	w := make([]int, len(t.headers))
	for i, h := range t.headers {
		w[i] = ansi.StringWidth(h)
	}
	for _, r := range t.rows {
		for i, c := range r {
			w[i] = max(w[i], ansi.StringWidth(presentation.SafeText(c.text)))
		}
	}
	used := 2 + len(columnGap)*(len(w)-1)
	for _, v := range w {
		used += v
	}
	if over := used - total; over > 0 {
		w[t.shrink] = max(12, w[t.shrink]-over)
	}
	return w
}

// render draws the header and rows [start, end); the cursor row is
// highlighted across the full width.
func (t table) render(width, cursor, start, end int) string {
	w := t.widths(width)
	var out strings.Builder
	line := func(cells []cell, header bool, selected bool) {
		var plain, styled strings.Builder
		for i, c := range cells {
			s := presentation.SafeText(c.text)
			if i == t.shrink {
				s = truncateMiddle(s, w[i])
			} else {
				s = ansi.Truncate(s, w[i], "…")
			}
			if i < len(cells)-1 {
				s += strings.Repeat(" ", w[i]-ansi.StringWidth(s)) + columnGap
			}
			plain.WriteString(s)
			styled.WriteString(c.style.Render(s))
		}
		switch {
		case header:
			out.WriteString("  " + headerStyle.Render(plain.String()))
		case selected:
			s := "› " + plain.String()
			if pad := width - ansi.StringWidth(s); pad > 0 {
				s += strings.Repeat(" ", pad)
			}
			out.WriteString(cursorStyle.Render(s))
		default:
			out.WriteString("  " + styled.String())
		}
		out.WriteString("\n")
	}
	head := make([]cell, len(t.headers))
	for i, h := range t.headers {
		head[i] = cell{text: h}
	}
	line(head, true, false)
	for i := start; i < min(end, len(t.rows)); i++ {
		line(t.rows[i], false, i == cursor)
	}
	return out.String()
}

// truncateMiddle keeps both ends of s: a pod name's distinguishing hash
// suffix stays visible when the name does not fit.
func truncateMiddle(s string, width int) string {
	if ansi.StringWidth(s) <= width {
		return s
	}
	tail := (width - 1) / 2
	head := width - 1 - tail
	return ansi.Truncate(s, head, "") + "…" + ansi.TruncateLeft(s, ansi.StringWidth(s)-tail, "")
}

func podRowCells(r presentation.Row) []cell {
	status := statusStyle(r.Status)
	name, ready := plainStyle, plainStyle
	if finished(r.Status) {
		name, ready = dimStyle, dimStyle
	} else if n, d, ok := strings.Cut(r.Ready, "/"); ok && n != d {
		ready = warnStyle
	}
	restarts := dimStyle
	if r.Restarts != "0" {
		restarts = warnStyle
	}
	return []cell{{r.Name, name}, {r.Ready, ready}, {r.Status, status}, {r.Restarts, restarts}, {r.Age, dimStyle}}
}

// statusStyle colours a kubectl STATUS value: healthy green, finished grey,
// in progress yellow, failed red.
func statusStyle(s string) lipgloss.Style {
	switch {
	case s == "Running":
		return okStyle
	case finished(s):
		return dimStyle
	case s == "Failed" || s == "Unknown" || s == "Evicted" || s == "NotReady" ||
		strings.Contains(s, "Err") || strings.Contains(s, "BackOff") || strings.Contains(s, "Kill") ||
		strings.Contains(s, "ExitCode") || strings.Contains(s, "Signal") || strings.Contains(s, "Error"):
		return badStyle
	default:
		return warnStyle
	}
}

func groupRowCells(r presentation.GroupRow) []cell {
	status := statusStyle(r.Worst)
	name, pods := boldStyle, plainStyle
	if finished(r.Worst) {
		name, pods = dimStyle, dimStyle
	} else if n, d, ok := strings.Cut(r.Pods, "/"); ok && n != d {
		pods = warnStyle
	}
	restarts := dimStyle
	if r.Restarts != "0" {
		restarts = warnStyle
	}
	return []cell{{r.Name, name}, {kindLabel(r.Kind), dimStyle}, {r.Pods, pods}, {r.Status, status}, {r.Restarts, restarts}, {r.Age, dimStyle}}
}
func kindLabel(k string) string {
	switch k {
	case "Deployment":
		return "deploy"
	case "StatefulSet":
		return "sts"
	case "DaemonSet":
		return "ds"
	case "CronJob":
		return "cron"
	}
	return strings.ToLower(k)
}

func finished(status string) bool { return status == "Completed" || status == "Succeeded" }

func containerStateStyle(state string, ready bool) lipgloss.Style {
	switch {
	case state == "Running" && ready:
		return okStyle
	case state == "Running":
		return warnStyle
	case strings.HasPrefix(state, "Terminated: Completed"):
		return dimStyle
	case strings.HasPrefix(state, "Terminated"), strings.Contains(state, "BackOff"), strings.Contains(state, "Err"):
		return badStyle
	default:
		return warnStyle
	}
}
