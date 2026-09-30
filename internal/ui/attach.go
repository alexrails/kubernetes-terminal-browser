package ui

import (
	"context"
	"fmt"
	"kubernetes-terminal-browser/internal/kube/kubectl"
	"kubernetes-terminal-browser/internal/pane"
	"kubernetes-terminal-browser/internal/presentation"
	"kubernetes-terminal-browser/internal/terminal"
)

// banner names a command that is about to take the terminal, and where.
func banner(kind, note string, prod bool) string {
	head := bannerStyle.Render(" ktb ")
	if prod {
		head += " " + prodStyle.Render(" PROD ")
	}
	return fmt.Sprintf("\n%s %s\n%s\n\n", head, titleStyle.Render(presentation.SafeText(kind)), dimStyle.Render(presentation.SafeText(note)))
}

// Attach runs a session handed over by the action menu in this pane's terminal. Reads
// use the terminal too, so a credential plugin can prompt here. Exec fetches
// the pod again and compares its UID: the pane starts later than the keypress
// that asked for it.
func Attach(ctx context.Context, s pane.Session, t terminal.IO) error {
	// herdr types the command into the pane's shell; clear that line away.
	fmt.Fprint(t.Out, "\x1b[H\x1b[2J\x1b[3J")
	fmt.Fprint(t.Out, banner("exec: "+s.Action.Label, s.Where+" · this pane closes when the command exits", s.Prod))
	c := kubectl.Client{Executable: s.Kubectl, Timeout: s.Timeout, Foreground: &t}
	return c.Exec(ctx, s.Ref, s.Action, t)
}
