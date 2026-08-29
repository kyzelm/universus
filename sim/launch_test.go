package sim

import "testing"

// Move-driven velocity. Before it, advanceState zeroed VX for every attack and
// never touched VY, so no move could carry the character anywhere — which is
// what blocked the uppercut and every advancing normal behind it.

// The arc is not authored. The move sets a velocity once and gravity does the
// rest, the same integration the jump goes through.
func TestALaunchingMoveRisesAndLands(t *testing.T) {
	s := New()
	s.Advance([2]uint16{InHP, 0})
	if s.Players[0].MoveIndex != 3 {
		t.Fatalf("the launcher did not come out: move %d", s.Players[0].MoveIndex)
	}

	mv := &char().Moves[3]
	peak, airborne := Fix(0), 0

	for range mv.Total() {
		s.Advance([2]uint16{0, 0})
		p := &s.Players[0]
		if p.Y > peak {
			peak = p.Y
		}
		if p.Y > GroundY {
			airborne++
		}
	}

	if peak <= 0 {
		t.Fatal("never left the ground")
	}
	if airborne < 10 {
		t.Errorf("airborne for %d frames, want an actual arc", airborne)
	}
	if got := s.Players[0].Y; got != GroundY {
		t.Errorf("ended at Y = %d, want back on the ground", got)
	}
	if s.Players[0].State == StateAttack {
		t.Error("the move outlasted its own recovery")
	}
}

// Forward is forward for both seats. Player 1 faces left, so their uppercut
// travels left.
func TestLaunchIsFacingRelative(t *testing.T) {
	s := New()
	s.Advance([2]uint16{InHP, InHP})

	x0, x1 := s.Players[0].X, s.Players[1].X
	for range 10 {
		s.Advance([2]uint16{0, 0})
	}

	if s.Players[0].X <= x0 {
		t.Errorf("player 0 went from %d to %d, want forward (right)", x0, s.Players[0].X)
	}
	if s.Players[1].X >= x1 {
		t.Errorf("player 1 went from %d to %d, want forward (left)", x1, s.Players[1].X)
	}
}

// A walk does not carry into the attack that comes out of it. Without the
// velocity handover in enterMove, every jab thrown while walking would drift.
func TestAnAttackDoesNotInheritWalkVelocity(t *testing.T) {
	s := New()
	start := s.Players[0].X
	for range 5 {
		s.Advance([2]uint16{InRight, 0})
	}
	if s.Players[0].X == start {
		t.Fatal("setup: the walk did not move anyone")
	}

	s.Advance([2]uint16{InLP, 0}) // a normal, no launch
	at := s.Players[0].X

	for range char().Moves[0].Total() - 1 {
		s.Advance([2]uint16{0, 0})
		if s.Players[0].X != at {
			t.Fatalf("the jab drifted from %d to %d", at, s.Players[0].X)
		}
	}
}

// A move that runs out while the character is still in the air hands over to
// StateAir. Idle is actionable, and actionable in mid-air is a different game.
func TestAMoveEndingInTheAirFallsOutIntoAJump(t *testing.T) {
	s := New()
	s.Advance([2]uint16{InHK, 0}) // total 6 frames, ~32 frames of airtime

	for range char().Moves[4].Total() {
		s.Advance([2]uint16{0, 0})
	}

	p := &s.Players[0]
	if p.Y <= GroundY {
		t.Fatalf("setup: back on the ground at Y = %d before the move ended", p.Y)
	}
	if p.State != StateAir {
		t.Fatalf("state %d after the move ended in the air, want StateAir", p.State)
	}

	// A pressed button in mid-air must do nothing, which is the whole point of
	// not being idle up there.
	s.Advance([2]uint16{InLP, 0})
	if s.Players[0].State == StateAttack {
		t.Error("attacked in mid-air")
	}

	for range 60 {
		s.Advance([2]uint16{0, 0})
		if s.Players[0].State == StateIdle {
			return
		}
	}
	t.Error("never landed")
}

// Landing stops horizontal momentum, so an uppercut does not skate through its
// recovery frames.
func TestLandingStopsTheSlide(t *testing.T) {
	s := New()
	s.Advance([2]uint16{InHP, 0})

	// Up and forward until it comes back down.
	for s.Players[0].Y > GroundY || s.Players[0].StateFrame < 2 {
		s.Advance([2]uint16{0, 0})
	}
	if s.Players[0].State != StateAttack {
		t.Fatal("setup: the move ended before it landed")
	}

	landed := s.Players[0].X
	s.Advance([2]uint16{0, 0})
	if s.Players[0].X != landed {
		t.Errorf("slid from %d to %d after landing", landed, s.Players[0].X)
	}
}

// The landing rule must not catch a move that never leaves the ground: an
// advancing normal is at ground level on every frame, and has to keep the
// velocity its data gave it for the whole move.
func TestAGroundedAdvancingMoveKeepsMoving(t *testing.T) {
	s := New()
	s.Advance([2]uint16{InMK, 0})
	if s.Players[0].MoveIndex != 5 {
		t.Fatalf("the advancing normal did not come out: move %d", s.Players[0].MoveIndex)
	}

	prev := s.Players[0].X
	for range char().Moves[5].Total() - 1 {
		s.Advance([2]uint16{0, 0})
		if s.Players[0].State != StateAttack {
			break
		}
		if s.Players[0].X <= prev {
			t.Fatalf("stopped advancing at X = %d", s.Players[0].X)
		}
		prev = s.Players[0].X
	}
	if s.Players[0].Y != GroundY {
		t.Errorf("left the ground at Y = %d", s.Players[0].Y)
	}
}

// Airborne is a fact about position, not only about state: everything that
// reads it — gravity, the landing, the air hurtbox — has to agree that a rising
// uppercut is in the air.
func TestAirborneFollowsThePlayerOffTheGround(t *testing.T) {
	s := New()
	s.Advance([2]uint16{InHP, 0})
	for range 5 {
		s.Advance([2]uint16{0, 0})
	}

	p := &s.Players[0]
	if p.State != StateAttack {
		t.Fatalf("state %d, want the attack still running", p.State)
	}
	if !p.Airborne() {
		t.Errorf("Y = %d but not airborne", p.Y)
	}
}
