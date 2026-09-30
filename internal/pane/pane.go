// Package pane opens a second ktb in a neighbouring terminal pane, so an exec
// session runs beside the browser instead of replacing it. The pane belongs to
// the multiplexer, not to this process: it outlives ktb by design, which is
// why it is neither an internal/terminal child nor tracked for shutdown.
package pane

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"kubernetes-terminal-browser/internal/config"
	"kubernetes-terminal-browser/internal/domain"
	"kubernetes-terminal-browser/internal/process"
	"strings"
	"time"
)

// Flag starts the ktb of a new pane. The encoded Session follows it, or is in
// the variable Var when the command line must not carry it.
const (
	Flag = "--pane-session"
	Var  = "KTB_PANE_SESSION"
)

// Session is one exec handed to the pane. It carries the pod UID, so the pane
// repeats the UID guard right before its exec.
type Session struct {
	Ref     domain.ContainerRef
	Action  config.Action
	Kubectl string
	Timeout time.Duration
	Where   string
	Prod    bool
	Env     map[string]string
	// Dir is the opener's working directory: a relative kubeconfig, kubectl
	// or PATH entry means the same file only from there.
	Dir string
}

// inherited are the variables that decide which kubeconfig kubectl reads and
// where it finds credential plugins. A pane gets the environment of the
// multiplexer's server, which may be older than the shell ktb was started from.
var inherited = []string{"KUBECONFIG", "PATH"}

// Environment captures the inherited variables that are set in this process.
func Environment(lookup func(string) (string, bool)) map[string]string {
	out := map[string]string{}
	for _, k := range inherited {
		if v, ok := lookup(k); ok {
			out[k] = v
		}
	}
	return out
}

// Apply makes the pane's process see the inherited variables as the ktb that
// opened it did: one that was unset there is unset here.
func (s Session) Apply(set func(string, string) error, unset func(string) error) {
	for _, k := range inherited {
		if v, ok := s.Env[k]; ok {
			_ = set(k, v)
		} else {
			_ = unset(k)
		}
	}
}

// Encode returns the session as one word without shell metacharacters.
func (s Session) Encode() (string, error) {
	b, e := json.Marshal(s)
	return base64.RawURLEncoding.EncodeToString(b), e
}
func Decode(v string) (Session, error) {
	var s Session
	b, e := base64.RawURLEncoding.DecodeString(v)
	if e == nil {
		e = json.Unmarshal(b, &s)
	}
	if e != nil {
		return s, fmt.Errorf("invalid %s value: %w", Flag, e)
	}
	if s.Kubectl == "" || s.Ref.Pod.Scope.Context == "" || s.Ref.Pod.Scope.Namespace == "" || s.Ref.Pod.Name == "" || s.Ref.Pod.UID == "" || s.Ref.Name == "" || len(s.Action.Argv) == 0 || s.Timeout < time.Second {
		return s, fmt.Errorf("invalid %s value: incomplete session", Flag)
	}
	return s, nil
}

// Opener is the command that creates a pane. Its argv takes the pane's command
// as trailing arguments and runs it without a shell; herdr instead splits
// first and types the command into the new pane's shell.
type Opener struct {
	Name  string
	Argv  []string
	Herdr bool
}

// Detect picks the opener: custom (config.yaml pane) wins, then the
// multiplexer named by the environment. tmux and zellij come first because
// they usually run inside the other terminals, not around them.
func Detect(env func(string) string, custom []string) (Opener, bool) {
	switch {
	case len(custom) > 0:
		return Opener{Name: custom[0], Argv: custom}, true
	case env("TMUX") != "":
		return Opener{Name: "tmux", Argv: []string{"tmux", "split-window", "-h"}}, true
	case env("ZELLIJ") != "":
		return Opener{Name: "zellij", Argv: []string{"zellij", "run", "--close-on-exit", "--direction", "right", "--"}}, true
	case env("HERDR_ENV") == "1":
		bin := env("HERDR_BIN_PATH")
		if bin == "" {
			bin = "herdr"
		}
		return Opener{Name: "herdr", Argv: []string{bin}, Herdr: true}, true
	case env("WEZTERM_PANE") != "":
		return Opener{Name: "WezTerm", Argv: []string{"wezterm", "cli", "split-pane", "--right", "--"}}, true
	// Without a socket kitty @ talks through the controlling terminal, which
	// the TUI is reading and a background process may not touch.
	case env("KITTY_LISTEN_ON") != "":
		return Opener{Name: "kitty", Argv: []string{"kitty", "@", "launch", "--location=vsplit", "--cwd=current"}}, true
	}
	return Opener{}, false
}

// Open creates the pane and starts the ktb executable self in it with the
// encoded session. It returns once the multiplexer has accepted the request,
// not when the session ends.
func (o Opener) Open(ctx context.Context, timeout time.Duration, self, session string) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if !o.Herdr {
		_, e := o.run(ctx, append(o.Argv[1:len(o.Argv):len(o.Argv)], self, Flag, session))
		return e
	}
	// The typed line ends up in the shell's history, so the session travels
	// in the pane's environment instead.
	out, e := o.run(ctx, []string{"pane", "split", "--current", "--direction", "right", "--focus", "--env", Var + "=" + session})
	if e != nil {
		return e
	}
	var v struct {
		Result struct {
			Pane struct {
				PaneID string `json:"pane_id"`
			}
		}
	}
	if e := json.Unmarshal(out, &v); e != nil || v.Result.Pane.PaneID == "" {
		return fmt.Errorf("%s: pane split returned no pane id", o.Name)
	}
	// exec replaces the pane's shell, so the pane closes with the session.
	_, e = o.run(ctx, []string{"pane", "run", v.Result.Pane.PaneID, "exec " + quote(self) + " " + Flag})
	return e
}
func (o Opener) run(ctx context.Context, args []string) ([]byte, error) {
	r := process.Run(ctx, o.Argv[0], args, nil)
	if r.Err == nil {
		return r.Stdout, nil
	}
	detail := strings.TrimSpace(string(r.Stderr))
	switch {
	case errors.Is(r.Err, context.DeadlineExceeded):
		detail = "timed out"
	case detail == "":
		detail = r.Err.Error()
	}
	return nil, fmt.Errorf("%s could not open a pane: %s", o.Name, detail)
}

// quote makes s one word for a POSIX-like shell.
func quote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
