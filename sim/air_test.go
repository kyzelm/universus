package sim

import "testing"

// jump puts player 0 in the air and returns the state on the first airborne
// frame. Pre-jump is grounded, so this waits it out.
func jump(t *testing.T, dir uint8) GameState {
	t.Helper()
	s := New()
	s.Advance([2]uint16{pad[dir], 0})
	for s.Players[0].State != StateAir {
		s.Advance([2]uint16{pad[dir], 0})
	}
	return s
}

// An air normal comes out in the air and the jump carries on underneath it.
// Without the velocity handover being skipped, pressing a button in mid-air
// would stop the jump dead.
func TestAnAirNormalRidesTheJump(t *testing.T) {
	s := jump(t, 9) // up-forward, so there is horizontal travel to lose

	s.Advance([2]uint16{InLP, 0})
	p := &s.Players[0]
	if p.State != StateAttack || p.MoveIndex != 6 {
		t.Fatalf("state %d move %d, want the air normal", p.State, p.MoveIndex)
	}

	x, y := p.X, p.Y
	s.Advance([2]uint16{0, 0})
	if s.Players[0].X == x {
		t.Error("the jump's horizontal travel stopped when the button was pressed")
	}
	if s.Players[0].Y == y {
		t.Error("the arc froze")
	}
	if !s.Players[0].Airborne() {
		t.Error("no longer airborne mid-air-normal")
	}
}

// An air move's recovery is the fall. It has no business continuing once the
// character is standing on the ground.
func TestLandingEndsAnAirNormal(t *testing.T) {
	s := jump(t, 8)

	// Press it on the way down and close to the ground, so the move is still
	// running when the jump ends — pressed at the apex it would simply expire
	// in mid-air and prove nothing about landing.
	for s.Players[0].VY > 0 || s.Players[0].Y > FromInt(20) {
		s.Advance([2]uint16{0, 0})
	}
	s.Advance([2]uint16{InLP, 0})
	if s.Players[0].MoveIndex != 6 {
		t.Fatalf("the air normal did not come out: move %d", s.Players[0].MoveIndex)
	}

	for range 60 {
		s.Advance([2]uint16{0, 0})
		if s.Players[0].Y == GroundY {
			break
		}
	}
	if p := &s.Players[0]; p.State != StateIdle {
		t.Errorf("state %d on the ground, want idle: the air normal outlived the jump", p.State)
	}
}

// A launching move is not an air move, and landing does not end it: those
// recovery frames on the ground are the punish window that makes an uppercut a
// risk rather than a free button.
func TestLandingDoesNotEndAnUppercut(t *testing.T) {
	s := New()
	s.Advance([2]uint16{InHP, 0}) // the fixture's launcher, move 3

	for range 60 {
		s.Advance([2]uint16{0, 0})
		if s.Players[0].Y == GroundY && s.Players[0].StateFrame > 2 {
			break
		}
	}

	p := &s.Players[0]
	if p.State != StateAttack || p.MoveIndex != 3 {
		t.Fatalf("state %d move %d after landing, want the uppercut still recovering", p.State, p.MoveIndex)
	}
}

// A jump is a commitment: nothing but a button is accepted up there.
func TestAJumpAcceptsNoDirections(t *testing.T) {
	for _, dir := range []uint8{2, 4, 6, 8} {
		s := jump(t, 8)
		for range 5 {
			s.Advance([2]uint16{pad[dir], 0})
		}
		if got := s.Players[0].State; got != StateAir {
			t.Errorf("holding %d in the air put the player in state %d", dir, got)
		}
	}
}

// The quarter-circle is still on the stick when the character leaves the
// ground. Without the ground/air gate in moveFor, the jump would throw a
// fireball.
func TestAGroundSpecialCannotComeOutInTheAir(t *testing.T) {
	s := New()
	feed(&s, 2, 3, 6) // the motion, then jump before the button
	s.Advance([2]uint16{pad[8], 0})
	for s.Players[0].State != StateAir {
		s.Advance([2]uint16{0, 0})
	}

	s.Advance([2]uint16{InLP, 0})
	if got := s.Players[0].MoveIndex; got != 6 {
		t.Errorf("move %d came out in the air, want the air normal 6", got)
	}
	for i := range s.Projectiles {
		if s.Projectiles[i].Active != 0 {
			t.Fatal("a fireball came out of a jump")
		}
	}
}

// And the reverse: an air normal is not available on the ground, so its button
// gives the grounded move instead.
func TestAnAirNormalIsNotAvailableOnTheGround(t *testing.T) {
	s := New()
	s.Advance([2]uint16{InLP, 0})
	if got := s.Players[0].MoveIndex; got != 0 {
		t.Errorf("move %d on the ground, want the standing jab 0", got)
	}
}
