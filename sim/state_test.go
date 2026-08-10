package sim

import (
	"testing"
	"unsafe"
)

// The state must stay flat and fixed-size: saveState/loadState are memcpys and
// the checksum is a hash over a byte range. This fails the moment someone adds
// a slice, a map or a pointer — which is exactly when it should fail.
func TestGameStateIsFlatAndFixedSize(t *testing.T) {
	const want = 4 + 2*4*4 // Frame + two players of four Fix
	if got := unsafe.Sizeof(GameState{}); got != want {
		t.Errorf("sizeof(GameState) = %d, want %d — did a field get added or padded?", got, want)
	}
	// Comparable at all: a slice/map/func field would make this line not compile.
	if New() == (GameState{}) {
		t.Error("New() returned the zero value; players should start apart")
	}
}

func run(s *GameState, frames int, in [2]uint16) {
	for range frames {
		s.Advance(in)
	}
}

func TestIdleIsStable(t *testing.T) {
	s := New()
	start := s
	run(&s, 600, [2]uint16{})
	for i, p := range s.Players {
		if p != start.Players[i] {
			t.Errorf("player %d drifted while idle: %+v -> %+v", i, start.Players[i], p)
		}
	}
	if s.Frame != 600 {
		t.Errorf("Frame = %d, want 600", s.Frame)
	}
}

func TestGravityNeverSinksBelowGround(t *testing.T) {
	s := New()
	for f := range 300 {
		s.Advance([2]uint16{InUp, InUp}) // hold up: jump, land, jump again
		for i, p := range s.Players {
			if p.Y < GroundY {
				t.Fatalf("frame %d: player %d fell through the floor: Y = %d", f, i, p.Y)
			}
		}
	}
}

func TestJumpRisesAndLands(t *testing.T) {
	s := New()
	s.Advance([2]uint16{InUp, 0})

	airborne, peak := 1, s.Players[0].Y
	for s.Players[0].Y > GroundY {
		s.Advance([2]uint16{}) // input released — the arc is committed
		airborne++
		if s.Players[0].Y > peak {
			peak = s.Players[0].Y
		}
	}

	// ~42 frames at -0.375/frame^2 from 8.0/frame. A constants change that
	// blows past this range is a balance decision, not an accident.
	if airborne < 38 || airborne > 46 {
		t.Errorf("jump lasted %d frames, want 38..46", airborne)
	}
	if peak.ToInt() < 80 {
		t.Errorf("jump peaked at %d units, want >= 80", peak.ToInt())
	}
	if s.Players[0].VY != 0 {
		t.Errorf("VY = %d after landing, want 0", s.Players[0].VY)
	}
}

func TestWallsClamp(t *testing.T) {
	s := New()
	run(&s, 1000, [2]uint16{InLeft, InRight})

	wantLeft := -StageHalfWidth + PlayerHalfWidth
	wantRight := StageHalfWidth - PlayerHalfWidth
	if s.Players[0].X != wantLeft {
		t.Errorf("player 0 X = %d, want %d", s.Players[0].X, wantLeft)
	}
	if s.Players[1].X != wantRight {
		t.Errorf("player 1 X = %d, want %d", s.Players[1].X, wantRight)
	}
}

func TestPlayersDoNotMerge(t *testing.T) {
	s := New()
	for f := range 500 {
		s.Advance([2]uint16{InRight, InLeft}) // walk into each other and keep pushing
		gap := (s.Players[1].X - s.Players[0].X).Abs()
		if gap < PlayerHalfWidth*2-One {
			t.Fatalf("frame %d: pushboxes merged, gap = %d units", f, gap.ToInt())
		}
	}
}

func TestJumpingOverAnotherPlayerIgnoresPushbox(t *testing.T) {
	s := New()
	s.Players[0].X = 0
	s.Players[1].X = 0
	s.Players[1].Y = PlayerHeight + One // clear overhead
	before := s.Players[0].X

	s.Advance([2]uint16{})
	if s.Players[0].X != before {
		t.Errorf("player 0 was pushed by a player stacked above it: %d -> %d", before, s.Players[0].X)
	}
}

// The whole architecture rests on this: same start, same inputs, same bytes.
func TestSameInputsProduceIdenticalState(t *testing.T) {
	inputs := inputSeq(2000)

	a, b := New(), New()
	for _, in := range inputs {
		a.Advance(in)
	}
	for _, in := range inputs {
		b.Advance(in)
	}
	if a != b {
		t.Fatalf("identical inputs diverged:\n a = %+v\n b = %+v", a, b)
	}
}
