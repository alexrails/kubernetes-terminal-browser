package auth

import "testing"

func TestAlwaysNeverBackground(t *testing.T) {
	s := New(true)
	if s.Background() {
		t.Fatal("Always started in background")
	}
	s.BeginForeground()
	s.EndForeground(true)
	if s.Background() || s.Mode != ManualOnly {
		t.Fatalf("Always resumed background: %+v", s)
	}
	s.BeginForeground()
	s.EndForeground(false)
	if s.Mode != ManualOnly {
		t.Fatalf("failed login moved Always out of manual: %+v", s)
	}
	s.Failure(true)
	if s.Mode != ManualOnly || s.Probe {
		t.Fatalf("credentials error moved Always out of manual: %+v", s)
	}
}
func TestRetryOnlyOneBackgroundProbe(t *testing.T) {
	s := New(false)
	s.Failure(true)
	if s.Mode != NeedsAuth {
		t.Fatal(s.Mode)
	}
	s.BeginForeground()
	s.EndForeground(true)
	if !s.Background() || !s.Probe {
		t.Fatalf("unexpected %+v", s)
	}
	s.Failure(true)
	if s.Mode != ManualOnly {
		t.Fatal(s.Mode)
	}
	// ManualOnly sticks: a later manual read is foreground only and never
	// schedules another probe, whether it succeeds or fails.
	s.BeginForeground()
	if s.Mode != ForegroundRetry {
		t.Fatal(s.Mode)
	}
	s.EndForeground(true)
	if s.Mode != ManualOnly || s.Probe || s.Background() {
		t.Fatalf("manual read re-enabled background: %+v", s)
	}
	s.BeginForeground()
	s.EndForeground(false)
	if s.Mode != ManualOnly || s.Probe {
		t.Fatalf("failed manual read left ManualOnly: %+v", s)
	}
	s.Failure(true)
	if s.Mode != ManualOnly {
		t.Fatalf("credentials error on a foreground operation left ManualOnly: %+v", s)
	}
}
func TestCanceledLoginKeepsNeedsAuth(t *testing.T) {
	s := New(false)
	s.Failure(true)
	s.BeginForeground()
	s.EndForeground(false)
	if s.Mode != NeedsAuth || s.Probe {
		t.Fatalf("unexpected %+v", s)
	}
	s.BeginForeground()
	s.EndForeground(true)
	if !s.Background() || !s.Probe {
		t.Fatalf("login after a canceled one did not allow the probe: %+v", s)
	}
	s.BackgroundSuccess()
	if s.Probe || !s.Background() {
		t.Fatalf("successful probe did not settle: %+v", s)
	}
}
func TestExplicitReenableStartsFresh(t *testing.T) {
	s := New(false)
	s.Failure(true)
	s.BeginForeground()
	s.EndForeground(true)
	s.Failure(true)
	if s.Mode != ManualOnly {
		t.Fatal(s.Mode)
	}
	s = New(false)
	if !s.Background() || s.Probe {
		t.Fatalf("re-enable is not a fresh BackgroundReady: %+v", s)
	}
}
