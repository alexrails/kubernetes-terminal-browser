package ui

import (
	tea "charm.land/bubbletea/v2"
	"errors"
	"kubernetes-terminal-browser/internal/kubeconfig"
	"kubernetes-terminal-browser/internal/pane"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// opener is a fake multiplexer that records the command it was asked to run.
func opener(t *testing.T) (pane.Opener, string) {
	t.Helper()
	dir := t.TempDir()
	bin, log := filepath.Join(dir, "mux"), filepath.Join(dir, "args")
	if e := os.WriteFile(bin, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > '"+log+"'\n"), 0700); e != nil {
		t.Fatal(e)
	}
	return pane.Opener{Name: "mux", Argv: []string{bin}}, log
}

// open runs cmd, batches included, and returns the pane result among its
// messages.
func open(t *testing.T, cmd tea.Cmd) opened {
	t.Helper()
	var found *opened
	var run func(tea.Cmd)
	run = func(c tea.Cmd) {
		if c == nil {
			return
		}
		switch v := c().(type) {
		case tea.BatchMsg:
			for _, c := range v {
				run(c)
			}
		case opened:
			found = &v
		}
	}
	run(cmd)
	if found == nil {
		t.Fatal("no pane was opened")
	}
	return *found
}

func TestPaneNeedsAMultiplexer(t *testing.T) {
	m := clusterModel()
	m.selectContext(kubeconfig.Context{Name: devCluster.ContextName(), Namespace: "shop-dev"})
	podsResult(m, deployPod("admin-web", "587dcb6845", "b1", "app"))
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if strings.Contains(screenText(m), "in a new pane") {
		t.Fatal("pane entries offered without a way to open a pane")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	if _, cmd := m.Update(key('3')); cmd != nil || m.screen != actionList || m.menuCursor != 1 || m.suspended {
		t.Fatalf("menu went past its actions: screen=%s cursor=%d", m.screen, m.menuCursor)
	}
}

func TestPaneRunsTheSelectedActionBeside(t *testing.T) {
	m := clusterModel()
	o, log := opener(t)
	m.Panes(o, "/opt/ktb")
	m.client.Executable, m.client.Timeout = "/bin/kubectl", 30*time.Second
	m.selectContext(kubeconfig.Context{Name: devCluster.ContextName(), Namespace: "shop-dev"})
	podsResult(m, deployPod("admin-web", "587dcb6845", "b1", "app"))
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if s := screenText(m); !strings.Contains(s, "in a new pane") || !strings.Contains(s, "4  Rails console") {
		t.Fatalf("pane entries not offered in the action menu:\n%s", s)
	}
	for range 3 {
		m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.suspended || m.screen != pods {
		t.Fatalf("the TUI gave up the terminal or kept the menu: suspended=%t screen=%s", m.suspended, m.screen)
	}
	r := open(t, cmd)
	if r.err != nil {
		t.Fatal(r.err)
	}
	b, _ := os.ReadFile(log)
	args := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(args) != 3 || args[0] != "/opt/ktb" || args[1] != pane.Flag {
		t.Fatalf("pane command = %q", args)
	}
	wd, _ := os.Getwd()
	s, e := pane.Decode(args[2])
	if e != nil || s.Ref.Pod.UID != "uid-b1" || s.Ref.Pod.Scope.Context != devCluster.ContextName() || s.Ref.Pod.Scope.Namespace != "shop-dev" || s.Ref.Name != "app" || s.Action.ID != "rails-console" || s.Kubectl != "/bin/kubectl" || s.Prod || s.Dir != wd {
		t.Fatalf("session = %+v, %v", s, e)
	}
	m.Update(r)
	if !strings.Contains(screenText(m), "Opened Rails console") {
		t.Fatalf("no confirmation:\n%s", screenText(m))
	}
}

func TestPaneOnProdAsksFirst(t *testing.T) {
	m := clusterModel()
	o, log := opener(t)
	m.Panes(o, "/opt/ktb")
	m.client.Executable, m.client.Timeout = "/bin/kubectl", 30*time.Second
	m.selectContext(kubeconfig.Context{Name: prodCluster.ContextName(), Namespace: "shop-prd"})
	podsResult(m, deployPod("admin-web", "587dcb6845", "b1", "app"))
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if _, cmd := m.Update(key('3')); cmd != nil || m.confirm == nil || !strings.Contains(screenText(m), "in a new pane in") {
		t.Fatalf("prod pane opened without confirmation:\n%s", screenText(m))
	}
	if _, cmd := m.Update(key('n')); cmd != nil || m.confirm != nil || m.beside {
		t.Fatal("n did not cancel")
	}
	if _, e := os.Stat(log); e == nil {
		t.Fatal("pane opened before y")
	}
	m.Update(key('3'))
	_, cmd := m.Update(key('y'))
	if r := open(t, cmd); r.err != nil || m.suspended || m.beside {
		t.Fatalf("y did not open the pane: %v", r.err)
	}
	b, _ := os.ReadFile(log)
	if s, e := pane.Decode(strings.Split(strings.TrimSpace(string(b)), "\n")[2]); e != nil || !s.Prod {
		t.Fatalf("pane session lost the prod mark: %+v, %v", s, e)
	}
}

func TestPaneFailureIsShownAfterLeavingTheScope(t *testing.T) {
	m := clusterModel()
	m.selectContext(kubeconfig.Context{Name: devCluster.ContextName(), Namespace: "shop-dev"})
	g := m.generation
	m.selectContext(kubeconfig.Context{Name: prodCluster.ContextName(), Namespace: "shop-prd"})
	m.Update(opened{generation: g, label: "Shell"})
	if m.sessionStatus != "" {
		t.Fatalf("late success shown in another scope: %q", m.sessionStatus)
	}
	m.Update(opened{generation: g, label: "Shell", err: errors.New("mux could not open a pane: no server")})
	if !strings.Contains(screenText(m), "no server") {
		t.Fatalf("pane failure lost:\n%s", screenText(m))
	}
}
