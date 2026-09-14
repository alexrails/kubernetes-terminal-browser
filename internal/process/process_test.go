package process

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestCancelKillsGrandchildAndReturns(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "fake-kubectl")
	pidfile := filepath.Join(dir, "grandchild.pid")
	script := "#!/bin/sh\ntrap '' TERM\n(trap '' TERM; sleep 30) &\necho $! > \"$KTB_PIDFILE\"\nwait\n"
	if e := os.WriteFile(bin, []byte(script), 0700); e != nil {
		t.Fatal(e)
	}
	t.Setenv("KTB_PIDFILE", pidfile)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan Result, 1)
	go func() { done <- Run(ctx, bin, nil, nil) }()
	var pid int
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		b, e := os.ReadFile(pidfile)
		if e == nil {
			pid, _ = strconv.Atoi(strings.TrimSpace(string(b)))
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if pid == 0 {
		cancel()
		t.Fatal("fake kubectl did not spawn")
	}
	cancel()
	select {
	case r := <-done:
		if r.Err != context.Canceled {
			t.Fatalf("got %+v", r)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("process did not stop")
	}
	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if syscall.Kill(pid, 0) == syscall.ESRCH {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Errorf("grandchild %d survived cancellation", pid)
}
