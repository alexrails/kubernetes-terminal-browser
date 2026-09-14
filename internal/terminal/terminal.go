// Package terminal runs commands only while Bubble Tea has released the TTY.
package terminal

import (
	"context"
	"fmt"
	"github.com/creack/pty"
	"golang.org/x/term"
	"io"
	"kubernetes-terminal-browser/internal/process"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type IO struct {
	In       io.Reader
	Out, Err io.Writer
}
type Session struct {
	IO IO
	Do func(IO) error
}

func (s *Session) SetStdin(r io.Reader)  { s.IO.In = r }
func (s *Session) SetStdout(w io.Writer) { s.IO.Out = w }
func (s *Session) SetStderr(w io.Writer) { s.IO.Err = w }
func (s *Session) Run() error            { return s.Do(s.IO) }

// Interactive children stay in the terminal's foreground process group. On
// application shutdown, kill only this child's tree, never the shared group.
func descendants(pid int) []int {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	data, e := exec.CommandContext(ctx, "ps", "-axo", "pid=,ppid=").Output()
	if e != nil {
		return nil
	}
	children := map[int][]int{}
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Fields(line)
		if len(f) != 2 {
			continue
		}
		p, _ := strconv.Atoi(f[0])
		parent, _ := strconv.Atoi(f[1])
		children[parent] = append(children[parent], p)
	}
	var out []int
	var walk func(int)
	walk = func(p int) {
		for _, c := range children[p] {
			walk(c)
			out = append(out, c)
		}
	}
	walk(pid)
	return out
}
func tee(w io.Writer, b *process.Buffer) io.Writer {
	if w == nil {
		return b
	}
	return io.MultiWriter(w, b)
}

// terminalStderr returns a TTY for a foreground child's stderr while relaying
// a bounded diagnostic copy to the application. Passing an io.MultiWriter to
// os/exec directly would make the child's fd 2 a pipe, which breaks kubectl and
// credential plugins that check whether stderr is a terminal.
func terminalStderr(t IO, diagnostic *process.Buffer) (io.Writer, func()) {
	f, ok := t.Err.(*os.File)
	if !ok || !term.IsTerminal(int(f.Fd())) {
		return tee(t.Err, diagnostic), func() {}
	}

	master, slave, err := pty.Open()
	if err != nil {
		// Preserving terminal semantics is more important than retaining a copy
		// of stderr when a secondary PTY cannot be allocated.
		return t.Err, func() {}
	}
	_ = pty.InheritSize(f, slave)
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(tee(t.Err, diagnostic), master)
		close(done)
	}()
	return slave, func() {
		_ = slave.Close()
		select {
		case <-done:
		case <-time.After(process.Grace):
			_ = master.Close()
			<-done
		}
		_ = master.Close()
	}
}

// Run executes a command with the user's terminal. Stderr always reaches the
// terminal and a bounded copy: the diagnostic of a foreground command (a
// Forbidden exec, a missing remote executable) is what the TUI classifies and
// shows after the screen is restored. With capture, stdout goes to a bounded
// buffer instead of the terminal.
func Run(ctx context.Context, executable string, args []string, t IO, capture bool) process.Result {
	return RunWithEnv(ctx, executable, args, t, capture, nil)
}

// RunWithEnv is Run with selected environment variables replaced for the
// child. It is used by gcloud so an explicit ktb --kubeconfig is also the file
// updated by get-credentials.
func RunWithEnv(ctx context.Context, executable string, args []string, t IO, capture bool, overrides map[string]string) process.Result {
	c := exec.CommandContext(ctx, executable, args...)
	if len(overrides) > 0 {
		env := os.Environ()
		for name, value := range overrides {
			prefix := name + "="
			filtered := env[:0]
			for _, item := range env {
				if !strings.HasPrefix(item, prefix) {
					filtered = append(filtered, item)
				}
			}
			env = append(filtered, prefix+value)
		}
		c.Env = env
	}
	c.Stdin = t.In
	c.Stdout = t.Out
	c.WaitDelay = 2 * process.Grace
	out := &process.Buffer{Limit: 32 << 20}
	diagnostic := &process.Buffer{Limit: 64 << 10}
	stderr, closeStderr := terminalStderr(t, diagnostic)
	c.Stderr = stderr
	if capture {
		c.Stdout = out
	}
	c.Cancel = func() error {
		pid := c.Process.Pid
		pids := append(descendants(pid), pid)
		for _, p := range pids {
			_ = syscall.Kill(p, syscall.SIGTERM)
		}
		time.Sleep(process.Grace)
		func() {
			for _, p := range pids {
				_ = syscall.Kill(p, syscall.SIGKILL)
			}
		}()
		return nil
	}
	e := c.Run()
	closeStderr()
	code := 0
	if e != nil {
		code = -1
		if c.ProcessState != nil {
			code = c.ProcessState.ExitCode()
		}
	}
	if ctx.Err() != nil {
		e = ctx.Err()
	}
	// Only captured output can be incomplete. The terminal already showed
	// everything a non-captured command wrote; its diagnostic copy is a
	// bounded excerpt by design, not a failure.
	truncated := capture && (out.Truncated || diagnostic.Truncated)
	return process.Result{Stdout: out.Bytes(), Stderr: diagnostic.Bytes(), Err: e, ExitCode: code, Truncated: truncated}
}

// WaitForEnter keeps the terminal with the user until Enter, so output a
// finished command left on screen can be read before Bubble Tea redraws. It
// returns at once when ctx ends (shutdown must not wait on a keypress) or when
// there is no stdin to read from.
func WaitForEnter(ctx context.Context, t IO, prompt string) {
	if t.In == nil || ctx.Err() != nil {
		return
	}
	if t.Out != nil {
		fmt.Fprint(t.Out, prompt)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		b := make([]byte, 1)
		for {
			n, e := t.In.Read(b)
			if e != nil || (n == 1 && b[0] == '\n') {
				return
			}
		}
	}()
	select {
	case <-done:
	case <-ctx.Done():
	}
}
