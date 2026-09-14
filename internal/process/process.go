// Package process owns background process groups and bounded output. Foreground
// terminal commands deliberately use a different lifecycle (internal/terminal).
package process

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

const Grace = 300 * time.Millisecond

type Result struct {
	Stdout, Stderr []byte
	ExitCode       int
	Err            error
	Truncated      bool
}
type Buffer struct {
	mu        sync.Mutex
	data      []byte
	Limit     int
	Truncated bool
}

func (b *Buffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(p)
	left := max(0, b.Limit-len(b.data))
	if len(p) > left {
		b.Truncated = true
		p = p[:left]
	}
	b.data = append(b.data, p...)
	return n, nil
}
func (b *Buffer) Bytes() []byte { b.mu.Lock(); defer b.mu.Unlock(); return bytes.Clone(b.data) }
func command(ctx context.Context, executable string, args []string) *exec.Cmd {
	c := exec.CommandContext(ctx, executable, args...)
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	c.WaitDelay = 2 * Grace
	c.Cancel = func() error {
		if c.Process == nil {
			return nil
		}
		pid := c.Process.Pid
		_ = syscall.Kill(-pid, syscall.SIGTERM)
		time.Sleep(Grace)
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		return nil
	}
	return c
}
func Run(ctx context.Context, executable string, args []string, stdout io.Writer) Result {
	if !register() {
		return Result{Err: context.Canceled, ExitCode: -1}
	}
	defer active.Done()
	c := command(ctx, executable, args)
	out := &Buffer{Limit: 32 << 20}
	errout := &Buffer{Limit: 64 << 10}
	if stdout == nil {
		stdout = out
	}
	c.Stdout = stdout
	c.Stderr = errout
	e := c.Run()
	// Clean up children even when their direct parent exits without waiting.
	if c.Process != nil {
		pid := c.Process.Pid
		if syscall.Kill(-pid, 0) == nil {
			_ = syscall.Kill(-pid, syscall.SIGTERM)
			time.Sleep(Grace)
			_ = syscall.Kill(-pid, syscall.SIGKILL)
		}
	}
	code := 0
	if e != nil {
		code = -1
		var ee *exec.ExitError
		if errors.As(e, &ee) {
			code = ee.ExitCode()
		}
	}
	if ctx.Err() != nil {
		e = ctx.Err()
	}
	return Result{Stdout: out.Bytes(), Stderr: errout.Bytes(), ExitCode: code, Err: e, Truncated: out.Truncated || errout.Truncated}
}

var active sync.WaitGroup
var registryMu sync.Mutex
var stopping bool

func register() bool {
	registryMu.Lock()
	defer registryMu.Unlock()
	if stopping {
		return false
	}
	active.Add(1)
	return true
}

// Shutdown is called after the application context is canceled. No new
// background process may start once shutdown begins.
func Shutdown() { registryMu.Lock(); stopping = true; registryMu.Unlock(); active.Wait() }
