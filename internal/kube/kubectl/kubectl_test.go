package kubectl

import (
	"bytes"
	"context"
	"errors"
	"kubernetes-terminal-browser/internal/config"
	"kubernetes-terminal-browser/internal/domain"
	"kubernetes-terminal-browser/internal/kube"
	"kubernetes-terminal-browser/internal/process"
	"kubernetes-terminal-browser/internal/terminal"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestArgvKeepsScopeAndActionSeparate(t *testing.T) {
	c := Client{Timeout: 15 * time.Second}
	s := domain.Scope{Context: "-ctx with space", Namespace: "ns", Kubeconfig: "/tmp/my config"}
	r := domain.ContainerRef{Pod: domain.PodRef{Scope: s, Name: "pod", UID: "old"}, Name: "main", Kind: "regular"}
	a := config.Action{Argv: []string{"sh", "-lc", "echo hi; $(touch /tmp/not-run)"}, Stdin: true, TTY: true}
	got := c.ExecArgs(r, a)
	want := []string{"--context=-ctx with space", "--kubeconfig=/tmp/my config", "--namespace=ns", "exec", "pod", "--container=main", "-i", "-t", "--", "sh", "-lc", "echo hi; $(touch /tmp/not-run)"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q want %q", got, want)
	}
	logs := c.LogsArgs(r, kube.LogsOptions{Tail: 200})
	if !strings.Contains(strings.Join(logs, "|"), "--container=main") {
		t.Fatalf("logs lost container: %q", logs)
	}
}
func TestFakeKubectlRefusesReplacedPod(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "kubectl")
	calls := filepath.Join(dir, "calls")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" >> \"$KTB_CALLS\"\ncase \" $* \" in\n  *' get pod '*) printf '%s\\n' '{\"metadata\":{\"name\":\"same\",\"uid\":\"replacement\"},\"spec\":{\"containers\":[{\"name\":\"main\"}]},\"status\":{\"phase\":\"Running\",\"containerStatuses\":[{\"name\":\"main\",\"state\":{\"running\":{}}}]}}' ;;\n  *) exit 127 ;;\nesac\n"
	if e := os.WriteFile(bin, []byte(script), 0700); e != nil {
		t.Fatal(e)
	}
	t.Setenv("KTB_CALLS", calls)
	c := Client{Executable: bin, Timeout: 5 * time.Second}
	r := domain.ContainerRef{Pod: domain.PodRef{Scope: domain.Scope{Context: "ctx", Namespace: "ns"}, Name: "same", UID: "original"}, Name: "main", Kind: "regular"}
	e := c.Exec(context.Background(), r, config.Action{Argv: []string{"sh"}}, terminal.IO{})
	var de *domain.Error
	if !errors.As(e, &de) || de.Kind != domain.Replaced {
		t.Fatalf("got %v", e)
	}
	b, e := os.ReadFile(calls)
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(b), "exec\n") || !strings.Contains(string(b), "--context=ctx\n") || !strings.Contains(string(b), "--namespace=ns\n") {
		t.Fatalf("unsafe calls: %s", b)
	}
}
func TestClassifyDoesNotRetryExit127(t *testing.T) {
	e := Classify("exec", domain.Scope{}, process.Result{Err: errors.New("exit status 127"), ExitCode: 127, Stderr: []byte("exit status 127")})
	var de *domain.Error
	if !errors.As(e, &de) || de.Kind != domain.Unknown || de.ExitCode != 127 {
		t.Fatal(e)
	}
}
func TestClassifyErrors(t *testing.T) {
	cases := []struct {
		message string
		kind    domain.ErrorKind
	}{{"Error from server (Forbidden): pods is forbidden", domain.Forbidden}, {"You must be logged in to the server (Unauthorized)", domain.Unauthorized}, {"getting credentials: exec: executable gcloud not found", domain.MissingExecutable}, {"dial tcp: connection refused", domain.Connection}, {"Error from server (NotFound): pods not found", domain.NotFound}, {"Unable to connect to the server: getting credentials: exec: executable gke-gcloud-auth-plugin not found\n\nIt looks like you are trying to use a client-go credential plugin that is not installed.", domain.MissingExecutable}, {"Unable to connect to the server: getting credentials: exec: executable gke-gcloud-auth-plugin failed with exit code 1", domain.Unauthorized}, {"getting credentials: exec: fork/exec /usr/local/bin/plugin: permission denied", domain.Unauthorized}}
	for _, tc := range cases {
		e := Classify("list", domain.Scope{}, process.Result{Err: errors.New("exit status 1"), ExitCode: 1, Stderr: []byte(tc.message)})
		var de *domain.Error
		if !errors.As(e, &de) || de.Kind != tc.kind {
			t.Errorf("%s: %v", tc.message, e)
		}
	}
}
func TestClassifyMissingKubectlBinary(t *testing.T) {
	e := Classify("list", domain.Scope{}, process.Result{Err: &exec.Error{Name: "kubectl", Err: exec.ErrNotFound}, ExitCode: -1})
	var de *domain.Error
	if !errors.As(e, &de) || de.Kind != domain.MissingExecutable {
		t.Fatal(e)
	}
}
func TestForegroundExecKeepsStderrDiagnostic(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "kubectl")
	script := "#!/bin/sh\ncase \" $* \" in\n  *' get pod '*) printf '%s\\n' '{\"metadata\":{\"name\":\"same\",\"uid\":\"original\"},\"spec\":{\"containers\":[{\"name\":\"main\"}]},\"status\":{\"phase\":\"Running\",\"containerStatuses\":[{\"name\":\"main\",\"state\":{\"running\":{}}}]}}' ;;\n  *' exec '*) printf '%s\\n' 'Error from server (Forbidden): pods \"same\" is forbidden: User \"dev\" cannot create resource \"pods/exec\"' >&2; exit 1 ;;\n  *) exit 127 ;;\nesac\n"
	if e := os.WriteFile(bin, []byte(script), 0700); e != nil {
		t.Fatal(e)
	}
	// Distinct writers, as Bubble Tea hands over its output and os.Stderr:
	// one bytes.Buffer under both would race inside io.Copy.
	var out, term bytes.Buffer
	tio := terminal.IO{Out: &out, Err: &term}
	c := Client{Executable: bin, Timeout: 5 * time.Second, Foreground: &tio}
	r := domain.ContainerRef{Pod: domain.PodRef{Scope: domain.Scope{Context: "ctx", Namespace: "ns"}, Name: "same", UID: "original"}, Name: "main", Kind: "regular"}
	e := c.Exec(context.Background(), r, config.Action{Argv: []string{"sh"}}, tio)
	var de *domain.Error
	if !errors.As(e, &de) || de.Kind != domain.Forbidden || !strings.Contains(de.Detail, "pods/exec") || de.ExitCode != 1 {
		t.Fatalf("foreground diagnostic lost: %v", e)
	}
	if !strings.Contains(term.String(), "pods/exec") {
		t.Fatal("stderr no longer reaches the terminal")
	}
}
func TestClassifyExecIgnoresRemoteOutput(t *testing.T) {
	cases := []struct {
		stderr string
		code   int
		kind   domain.ErrorKind
	}{
		{"sh: mycli: not found\ncommand terminated with exit code 127", 127, domain.Unknown},
		{"Unauthorized: token expired\nYou must be logged in\ncommand terminated with exit code 1", 1, domain.Unknown},
		{"standard input is not a terminal\ngetting credentials failed", 1, domain.Unknown},
		// cobra-style and kubectl-in-a-toolbox-pod output after the remote process ran
		{"Error: config file /etc/mycli.yaml not found\ncommand terminated with exit code 1", 1, domain.Unknown},
		{"Error: Unauthorized\ncommand terminated with exit code 1", 1, domain.Unknown},
		{"error: You must be logged in to the server (Unauthorized)\ncommand terminated with exit code 1", 1, domain.Unknown},
		{"Error from server (NotFound): pods \"gone\" not found\n  command terminated with exit code 1", 1, domain.Unknown},
		{"error: unable to upgrade connection: container not found (\"main\")", 1, domain.NotFound},
		{"Error from server (Forbidden): pods \"x\" is forbidden: User \"dev\" cannot create resource \"pods/exec\"", 1, domain.Forbidden},
		{"Error from server (NotFound): pods \"gone\" not found", 1, domain.NotFound},
		{"error: You must be logged in to the server (Unauthorized)", 1, domain.Unauthorized},
		{"Unable to connect to the server: getting credentials: exec: executable gke-gcloud-auth-plugin failed with exit code 1", 1, domain.Unauthorized},
		{"Unable to connect to the server: getting credentials: exec: executable gke-gcloud-auth-plugin not found", 1, domain.MissingExecutable},
		{"Unable to connect to the server: dial tcp 10.0.0.1:6443: i/o timeout", 1, domain.Connection},
	}
	for _, tc := range cases {
		e := ClassifyExec("exec", domain.Scope{}, process.Result{Err: errors.New("exit status"), ExitCode: tc.code, Stderr: []byte(tc.stderr)})
		var de *domain.Error
		first := strings.SplitN(tc.stderr, "\n", 2)[0]
		if !errors.As(e, &de) || de.Kind != tc.kind || de.ExitCode != tc.code || !strings.Contains(de.Detail, first) {
			t.Errorf("%q: %v", tc.stderr, e)
		}
	}
	if e := ClassifyExec("exec", domain.Scope{}, process.Result{}); e != nil {
		t.Fatal(e)
	}
	var de *domain.Error
	if e := ClassifyExec("exec", domain.Scope{}, process.Result{Err: context.Canceled, ExitCode: -1}); !errors.As(e, &de) || de.Kind != domain.Canceled {
		t.Fatal(e)
	}
	if e := ClassifyExec("exec", domain.Scope{}, process.Result{Err: &exec.Error{Name: "kubectl", Err: exec.ErrNotFound}, ExitCode: -1}); !errors.As(e, &de) || de.Kind != domain.MissingExecutable {
		t.Fatal(e)
	}
}
func TestRemoteExecFailureStaysUnknown(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "kubectl")
	script := "#!/bin/sh\ncase \" $* \" in\n  *' get pod '*) printf '%s\\n' '{\"metadata\":{\"name\":\"same\",\"uid\":\"original\"},\"spec\":{\"containers\":[{\"name\":\"main\"}]},\"status\":{\"phase\":\"Running\",\"containerStatuses\":[{\"name\":\"main\",\"state\":{\"running\":{}}}]}}' ;;\n  *' exec '*) printf '%s\\n' 'sh: mycli: not found' 'Error: Unauthorized' 'command terminated with exit code 127' >&2; exit 127 ;;\n  *) exit 127 ;;\nesac\n"
	if e := os.WriteFile(bin, []byte(script), 0700); e != nil {
		t.Fatal(e)
	}
	var out, term bytes.Buffer
	tio := terminal.IO{Out: &out, Err: &term}
	c := Client{Executable: bin, Timeout: 5 * time.Second, Foreground: &tio}
	r := domain.ContainerRef{Pod: domain.PodRef{Scope: domain.Scope{Context: "ctx", Namespace: "ns"}, Name: "same", UID: "original"}, Name: "main", Kind: "regular"}
	e := c.Exec(context.Background(), r, config.Action{Argv: []string{"sh", "-lc", "mycli"}, Stdin: false, TTY: false}, tio)
	var de *domain.Error
	if !errors.As(e, &de) || de.Kind != domain.Unknown || de.ExitCode != 127 || !strings.Contains(de.Detail, "mycli") {
		t.Fatalf("remote output drove the classification: %v", e)
	}
}
