package sim

import "testing"

// Training mode (03 Game Design/Game Modes.md, D41). The lab: resources refill,
// the clock does not run, the round never ends. Everything else is the same
// game — which is the point of practising in it.

func TestTrainingStopsTheClockAndTheRound(t *testing.T) {
	s := NewTraining()
	timer := s.Timer

	// Empty a health bar and run well past the point a round would end.
	s.Players[1].Health = 0
	for f := 0; f < 120; f++ {
		s.Advance([2]uint16{0, 0})
	}

	if s.Timer != timer {
		t.Errorf("timer moved from %d to %d: the lab has no clock", timer, s.Timer)
	}
	if s.Phase != PhaseFight {
		t.Errorf("phase = %d, want the fight: the round ended in training", s.Phase)
	}
	if s.Wins[0] != 0 {
		t.Errorf("player 0 was awarded %d rounds in training", s.Wins[0])
	}
}

func TestTrainingRefillsResources(t *testing.T) {
	s := NewTraining()
	s.Players[0].Health = 1
	s.Players[0].Drive = 0
	s.Players[0].Burnout = 1
	s.Players[0].Super = 0

	s.Advance([2]uint16{0, 0})

	p := s.Players[0]
	if p.Health != char().Health || p.Drive != DriveMax || p.Super != SuperMax || p.Burnout != 0 {
		t.Errorf("not topped up: health %d drive %d super %d burnout %d",
			p.Health, p.Drive, p.Super, p.Burnout)
	}
}

// The refill waits for the consequences to finish. A bar that filled back in
// during the combo would hide the damage the combo did, which is the number the
// person practising is watching.
func TestTrainingDoesNotRefillMidCombo(t *testing.T) {
	s := NewTraining()
	s.Players[0].X, s.Players[1].X = FromInt(-20), FromInt(20)

	for f := 0; f < 10 && s.Players[1].State != StateHitstun; f++ {
		s.Advance([2]uint16{InLP, 0})
	}
	if s.Players[1].State != StateHitstun {
		t.Fatalf("player 1 is in state %d, want hitstun", s.Players[1].State)
	}

	if s.Players[1].Health == char().Health {
		t.Error("health was refilled during the hitstun it was lost in")
	}
}

// Off by default, and the flag is in the state, so the checksum covers it: two
// clients that disagreed about the mode desync on frame 0 rather than playing a
// match where one of them has infinite meter.
func TestTrainingIsOffByDefaultAndCoveredByTheChecksum(t *testing.T) {
	normal, lab := New(), NewTraining()
	if normal.Training != 0 {
		t.Error("an ordinary match started in training mode")
	}
	if normal.Checksum() == lab.Checksum() {
		t.Error("the checksum does not cover the mode")
	}
}
