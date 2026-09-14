package terminal

import (
	"bytes"
	"context"
	"github.com/creack/pty"
	"io"
	"strings"
	"testing"
	"time"
)

func TestRunKeepsForegroundDescriptorsOnTTY(t *testing.T) {
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer master.Close()
	defer slave.Close()

	tio := IO{In: slave, Out: slave, Err: slave}
	for _, tc := range []struct {
		name    string
		capture bool
		check   string
	}{
		{name: "interactive command", check: "test -t 0 && test -t 1 && test -t 2; printf diagnostic >&2"},
		{name: "captured read", capture: true, check: "test -t 0 && ! test -t 1 && test -t 2; printf diagnostic >&2; printf '{}\\n'"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := Run(context.Background(), "/bin/sh", []string{"-c", tc.check}, tio, tc.capture)
			if r.Err != nil || r.ExitCode != 0 {
				t.Fatalf("foreground descriptors lost their TTY: %+v", r)
			}
			if !strings.Contains(string(r.Stderr), "diagnostic") {
				t.Fatalf("foreground diagnostic was not relayed: %q", r.Stderr)
			}
			if tc.capture && string(r.Stdout) != "{}\n" {
				t.Fatalf("stdout was not captured: %q", r.Stdout)
			}
		})
	}
}

func TestRunKeepsForegroundStderrDiagnostic(t *testing.T) {
	var term bytes.Buffer
	r := Run(context.Background(), "/bin/sh", []string{"-c", `echo 'Error from server (Forbidden): pods "x" is forbidden' >&2; exit 1`}, IO{Err: &term}, false)
	if r.ExitCode != 1 || r.Err == nil {
		t.Fatalf("unexpected result %+v", r)
	}
	if !strings.Contains(string(r.Stderr), "Forbidden") {
		t.Fatalf("diagnostic lost without capture: %q", r.Stderr)
	}
	if !strings.Contains(term.String(), "Forbidden") {
		t.Fatal("stderr no longer reaches the terminal")
	}
	if r.Truncated {
		t.Fatal("non-captured command reported truncation")
	}
}
func TestRunNonCapturedOutputIsBoundedButNotTruncated(t *testing.T) {
	r := Run(context.Background(), "/bin/sh", []string{"-c", "head -c 100000 /dev/zero | tr '\\0' x >&2"}, IO{Err: io.Discard}, false)
	if r.Err != nil || r.ExitCode != 0 {
		t.Fatalf("unexpected result %+v", r)
	}
	if r.Truncated {
		t.Fatal("a successful foreground command was reported as truncated")
	}
	if len(r.Stderr) != 64<<10 {
		t.Fatalf("diagnostic copy is not bounded: %d bytes", len(r.Stderr))
	}
}
func TestRunNilTerminalWritersStillCollectDiagnostic(t *testing.T) {
	r := Run(context.Background(), "/bin/sh", []string{"-c", "echo oops >&2; exit 3"}, IO{}, true)
	if r.ExitCode != 3 || !strings.Contains(string(r.Stderr), "oops") {
		t.Fatalf("unexpected result %+v", r)
	}
}
func TestWaitForEnterReturnsOnNewlineOrCancel(t *testing.T) {
	pr, pw := io.Pipe()
	var out bytes.Buffer
	done := make(chan struct{})
	go func() { WaitForEnter(context.Background(), IO{In: pr, Out: &out}, "Press Enter"); close(done) }()
	if _, e := pw.Write([]byte("x\n")); e != nil {
		t.Fatal(e)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("did not return on Enter")
	}
	if !strings.Contains(out.String(), "Press Enter") {
		t.Fatal("prompt not shown")
	}
	pr2, pw2 := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	done2 := make(chan struct{})
	go func() { WaitForEnter(ctx, IO{In: pr2}, ""); close(done2) }()
	cancel()
	select {
	case <-done2:
	case <-time.After(2 * time.Second):
		t.Fatal("did not return on cancel")
	}
	_ = pw2.Close()
	WaitForEnter(context.Background(), IO{}, "") // no stdin: returns at once
}

func TestRunWithEnvReplacesValue(t *testing.T) {
	r := RunWithEnv(context.Background(), "/bin/sh", []string{"-c", `printf %s "$KUBECONFIG"`}, IO{}, true, map[string]string{"KUBECONFIG": "/tmp/ktb-explicit-config"})
	if r.Err != nil {
		t.Fatal(r.Err)
	}
	if got := string(r.Stdout); got != "/tmp/ktb-explicit-config" {
		t.Fatalf("KUBECONFIG = %q", got)
	}
}
