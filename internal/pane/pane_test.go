package pane

import (
	"context"
	"kubernetes-terminal-browser/internal/config"
	"kubernetes-terminal-browser/internal/domain"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestDetect(t *testing.T) {
	for _, c := range []struct {
		name   string
		env    map[string]string
		custom []string
		want   string
		argv0  string
	}{
		{name: "none"},
		{name: "custom wins", env: map[string]string{"TMUX": "/tmp/tmux-1/default,1,0"}, custom: []string{"mysplit", "-x"}, want: "mysplit", argv0: "mysplit"},
		{name: "tmux inside herdr", env: map[string]string{"TMUX": "x", "HERDR_ENV": "1"}, want: "tmux", argv0: "tmux"},
		{name: "zellij", env: map[string]string{"ZELLIJ": "0"}, want: "zellij", argv0: "zellij"},
		{name: "herdr binary of the running app", env: map[string]string{"HERDR_ENV": "1", "HERDR_BIN_PATH": "/opt/herdr"}, want: "herdr", argv0: "/opt/herdr"},
		{name: "herdr from PATH", env: map[string]string{"HERDR_ENV": "1"}, want: "herdr", argv0: "herdr"},
		{name: "herdr off", env: map[string]string{"HERDR_ENV": "0"}},
		{name: "wezterm", env: map[string]string{"WEZTERM_PANE": "3"}, want: "WezTerm", argv0: "wezterm"},
		{name: "kitty over a socket", env: map[string]string{"KITTY_WINDOW_ID": "1", "KITTY_LISTEN_ON": "unix:/tmp/kitty"}, want: "kitty", argv0: "kitty"},
		{name: "kitty over the terminal", env: map[string]string{"KITTY_WINDOW_ID": "1"}},
	} {
		o, ok := Detect(func(k string) string { return c.env[k] }, c.custom)
		if ok != (c.want != "") || o.Name != c.want || (ok && o.Argv[0] != c.argv0) {
			t.Errorf("%s: got %+v, %t", c.name, o, ok)
		}
	}
}

func TestSessionRoundTrip(t *testing.T) {
	s := Session{
		Ref:     domain.ContainerRef{Pod: domain.PodRef{Scope: domain.Scope{Kubeconfig: "/k c/config", Context: "gke_p_r_c", Namespace: "shop"}, Name: "web-1", UID: "uid-1"}, Name: "app", Kind: "regular"},
		Action:  config.Action{ID: "x", Label: "It's $HOME", Argv: []string{"sh", "-lc", "echo 'a b' \"$X\""}, Stdin: true, TTY: true},
		Kubectl: "/usr/local/bin/kubectl", Timeout: 15 * time.Second, Where: "c › shop › web-1 / app", Prod: true,
		Env: map[string]string{"KUBECONFIG": "/a/config:/b c/config"}, Dir: "/work/my app",
	}
	v, e := s.Encode()
	if e != nil || strings.Trim(v, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_") != "" {
		t.Fatalf("encoded session is not one shell-safe word: %q, %v", v, e)
	}
	got, e := Decode(v)
	if e != nil || !reflect.DeepEqual(got, s) {
		t.Fatalf("round trip: %+v, %v", got, e)
	}
	noUID := s
	noUID.Ref.Pod.UID = ""
	bad, _ := noUID.Encode()
	for _, v := range []string{"", "not base64!", "e30", bad} {
		if _, e := Decode(v); e == nil {
			t.Errorf("accepted %q", v)
		}
	}
}

// fake writes an executable that appends its arguments, one per line and
// closed by "--end--", to the returned log, then runs body.
func fake(t *testing.T, body string) (bin, log string) {
	t.Helper()
	dir := t.TempDir()
	bin, log = filepath.Join(dir, "mux"), filepath.Join(dir, "args")
	script := "#!/bin/sh\nfor a in \"$@\"; do printf '%s\\n' \"$a\"; done >> '" + log + "'\necho --end-- >> '" + log + "'\n" + body + "\n"
	if e := os.WriteFile(bin, []byte(script), 0700); e != nil {
		t.Fatal(e)
	}
	return bin, log
}
func calls(t *testing.T, log string) [][]string {
	t.Helper()
	b, _ := os.ReadFile(log)
	var out [][]string
	for _, c := range strings.Split(string(b), "--end--\n") {
		if c != "" {
			out = append(out, strings.Split(strings.TrimSuffix(c, "\n"), "\n"))
		}
	}
	return out
}

func TestEnvironmentFollowsTheOpeningProcess(t *testing.T) {
	src := map[string]string{"KUBECONFIG": "/a/config:/b/config", "HOME": "/home/x"}
	s := Session{Env: Environment(func(k string) (string, bool) { v, ok := src[k]; return v, ok })}
	if !reflect.DeepEqual(s.Env, map[string]string{"KUBECONFIG": "/a/config:/b/config"}) {
		t.Fatalf("captured %q", s.Env)
	}
	pane := map[string]string{"KUBECONFIG": "/tmux/server/config", "PATH": "/old/bin", "HOME": "/home/x"}
	s.Apply(func(k, v string) error { pane[k] = v; return nil }, func(k string) error { delete(pane, k); return nil })
	if !reflect.DeepEqual(pane, src) {
		t.Fatalf("pane environment %q, want %q", pane, src)
	}
}

func TestOpenAppendsCommandAsArguments(t *testing.T) {
	bin, log := fake(t, "")
	o := Opener{Name: "mux", Argv: []string{bin, "split", "--"}}
	cmd := []string{"/opt/my tools/ktb", Flag, "abc"}
	if e := o.Open(context.Background(), 5*time.Second, cmd[0], cmd[2]); e != nil {
		t.Fatal(e)
	}
	if got, want := calls(t, log), [][]string{append([]string{"split", "--"}, cmd...)}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
	if len(o.Argv) != 3 {
		t.Fatalf("Open changed the opener: %q", o.Argv)
	}
}

// The line herdr types is kept by the shell's history, so it must not hold the
// session: only the environment of the new pane does.
func TestOpenHerdrSplitsThenRuns(t *testing.T) {
	bin, log := fake(t, `[ "$2" = split ] && echo '{"result":{"pane":{"pane_id":"w1:p7"}}}'; exit 0`)
	o := Opener{Name: "herdr", Argv: []string{bin}, Herdr: true}
	if e := o.Open(context.Background(), 5*time.Second, "/opt/it's/ktb", "abc"); e != nil {
		t.Fatal(e)
	}
	want := [][]string{
		{"pane", "split", "--current", "--direction", "right", "--focus", "--env", Var + "=abc"},
		{"pane", "run", "w1:p7", `exec '/opt/it'\''s/ktb' ` + Flag},
	}
	if got := calls(t, log); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestOpenFailures(t *testing.T) {
	bin, _ := fake(t, "echo 'no server running' >&2; exit 1")
	e := Opener{Name: "mux", Argv: []string{bin}}.Open(context.Background(), 5*time.Second, "ktb", "abc")
	if e == nil || !strings.Contains(e.Error(), "mux could not open a pane: no server running") {
		t.Fatalf("stderr of the opener not reported: %v", e)
	}
	bin, log := fake(t, "echo '{}'")
	e = Opener{Name: "herdr", Argv: []string{bin}, Herdr: true}.Open(context.Background(), 5*time.Second, "ktb", "abc")
	if e == nil || len(calls(t, log)) != 1 {
		t.Fatalf("ran a command without a pane id: %v, %q", e, calls(t, log))
	}
	e = Opener{Name: "mux", Argv: []string{filepath.Join(t.TempDir(), "absent")}}.Open(context.Background(), 5*time.Second, "ktb", "abc")
	if e == nil {
		t.Fatal("missing executable accepted")
	}
}
