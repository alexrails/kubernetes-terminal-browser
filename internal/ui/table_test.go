package ui

import (
	"github.com/charmbracelet/x/ansi"
	"kubernetes-terminal-browser/internal/presentation"
	"strings"
	"testing"
)

func TestTableColumnsAlignForLongNames(t *testing.T) {
	tb := table{headers: []string{"NAME", "READY", "STATUS", "RESTARTS", "AGE"}}
	for _, r := range []presentation.Row{
		{Name: "billing-admin-67b6589588-fqmfd", Ready: "1/1", Status: "Running", Restarts: "0", Age: "11m"},
		{Name: "formula-engine-user-attributes-reader-677554c4f5-bsnk6", Ready: "1/1", Status: "Running", Restarts: "0", Age: "131m"},
		{Name: "broken-6d8b88987f-58qdl", Ready: "0/1", Status: "CrashLoopBackOff", Restarts: "7 (2m ago)", Age: "1h"},
	} {
		tb.rows = append(tb.rows, podRowCells(r))
	}
	for _, width := range []int{140, 70} {
		lines := strings.Split(strings.TrimRight(ansi.Strip(tb.render(width, 1, 0, 10)), "\n"), "\n")
		at := func(l, sub string) int { return ansi.StringWidth(l[:strings.Index(l, sub)]) }
		col := at(lines[0], "READY")
		for _, l := range lines[1:] {
			if got := at(l, "/1 ") - 1; got != col {
				t.Fatalf("width %d: READY column at %d, want %d:\n%s", width, got, col, strings.Join(lines, "\n"))
			}
			if ansi.StringWidth(strings.TrimRight(l, " ")) > width {
				t.Fatalf("width %d: row wider than terminal: %q", width, l)
			}
		}
		if width == 70 && !strings.Contains(lines[2], "…") || !strings.Contains(lines[2], "-bsnk6 ") {
			t.Fatalf("long name lost its unique suffix: %q", lines[2])
		}
	}
}
