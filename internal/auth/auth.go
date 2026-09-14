package auth

type Mode string

const (
	BackgroundReady Mode = "BackgroundReady"
	NeedsAuth       Mode = "NeedsAuth"
	ForegroundRetry Mode = "ForegroundRetry"
	ManualOnly      Mode = "ManualOnly"
)

// State is the access mode of one context. ManualOnly is sticky: once a
// context is there (an Always plugin, or a background probe that needed
// interactive credentials again) nothing but an explicit re-enable leaves it,
// so a foreground read never schedules another background probe.
type State struct {
	Mode          Mode
	Always, Probe bool
	prior         Mode // the mode the current foreground read started from
}

func New(always bool) State {
	s := State{Mode: BackgroundReady, Always: always}
	if always {
		s.Mode = ManualOnly
	}
	return s
}
func (s State) Background() bool { return s.Mode == BackgroundReady }

// Failure records a failed read. Only a credentials error changes the mode: it
// stops background reads, and a context already in ManualOnly stays there.
func (s *State) Failure(credentials bool) {
	if !credentials || s.Mode == ManualOnly {
		return
	}
	if s.Probe {
		s.Mode = ManualOnly
	} else {
		s.Mode = NeedsAuth
	}
	s.Probe = false
}
func (s *State) BeginForeground() {
	if s.Mode != ForegroundRetry {
		s.prior = s.Mode
	}
	s.Mode = ForegroundRetry
}

// EndForeground returns from a foreground read. A context that was ManualOnly,
// or is Always, goes back to ManualOnly whatever the outcome: only an explicit
// re-enable turns background reads on again. Otherwise success allows one
// background probe and a failed or canceled login keeps NeedsAuth.
func (s *State) EndForeground(ok bool) {
	prior := s.prior
	s.prior = ""
	if s.Always || prior == ManualOnly {
		s.Mode = ManualOnly
		s.Probe = false
		return
	}
	if !ok {
		s.Mode = NeedsAuth
		return
	}
	s.Mode = BackgroundReady
	s.Probe = true
}
func (s *State) BackgroundSuccess() { s.Probe = false }
