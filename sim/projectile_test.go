package sim

import "testing"

// fireball puts player 0 through QCF+LP and returns the state on the frame the
// projectile appears. The fixture's special is the third move.
func fireball(t *testing.T) GameState {
	t.Helper()
	s := New()
	feed(&s, 2, 3)
	s.Advance([2]uint16{pad[6] | InLP, 0})
	if s.Players[0].MoveIndex != 2 {
		t.Fatalf("the fireball did not come out: move %d", s.Players[0].MoveIndex)
	}

	mv := &char().Moves[2]
	for s.Players[0].StateFrame < mv.Startup {
		s.Advance([2]uint16{0, 0})
	}
	return s
}

func live(s *GameState) int {
	n := 0
	for i := range s.Projectiles {
		if s.Projectiles[i].Active != 0 {
			n++
		}
	}
	return n
}

// mirror swaps left and right in a bitfield, so a test can give player 1 the
// same facing-relative input as player 0.
func mirror(bits uint16) uint16 {
	out := bits &^ (InLeft | InRight)
	if bits&InLeft != 0 {
		out |= InRight
	}
	if bits&InRight != 0 {
		out |= InLeft
	}
	return out
}

// The spawn frame is the move's first active frame — data the move already
// carries, so it cannot drift from the frame data the way a separate field
// would.
func TestFireballSpawnsOnTheActiveFrame(t *testing.T) {
	s := New()
	feed(&s, 2, 3)
	s.Advance([2]uint16{pad[6] | InLP, 0})

	mv := &char().Moves[2]
	for f := int32(1); f < mv.Startup; f++ {
		if live(&s) != 0 {
			t.Fatalf("a projectile existed on move frame %d, before startup %d", f, mv.Startup)
		}
		s.Advance([2]uint16{0, 0})
	}
	if live(&s) != 1 {
		t.Fatalf("no projectile on the first active frame, %d live", live(&s))
	}

	pr := &s.Projectiles[0]
	if want := s.Players[0].X + mv.Proj.SpawnX; pr.X != want {
		t.Errorf("spawned at %d, want %d (owner + spawn offset)", pr.X, want)
	}
	if pr.Owner != 0 || pr.Move != 2 {
		t.Errorf("owner %d move %d, want 0 and 2", pr.Owner, pr.Move)
	}
}

// It travels at the move's speed, one step a frame, and it does not move on the
// frame it appears.
func TestFireballTravelsAtItsSpeed(t *testing.T) {
	s := fireball(t)
	speed := char().Moves[2].Proj.Speed

	prev := s.Projectiles[0].X
	for range 10 {
		s.Advance([2]uint16{0, 0})
		got := s.Projectiles[0].X
		if got-prev != speed {
			t.Fatalf("moved %d in one frame, want %d", got-prev, speed)
		}
		prev = got
	}
}

// Facing is the direction of travel, taken at the spawn. The owner may turn
// around afterwards; the fireball does not turn with them.
func TestFireballMirrorsWithFacing(t *testing.T) {
	s := New()
	for _, d := range []uint8{2, 3} {
		s.Advance([2]uint16{0, mirror(pad[d])})
	}
	s.Advance([2]uint16{0, mirror(pad[6]) | InLP})
	if s.Players[1].MoveIndex != 2 {
		t.Fatalf("player 1's fireball did not come out: move %d", s.Players[1].MoveIndex)
	}

	mv := &char().Moves[2]
	for s.Players[1].StateFrame < mv.Startup {
		s.Advance([2]uint16{0, 0})
	}

	pr := &s.Projectiles[0]
	if pr.Owner != 1 {
		t.Fatalf("owner %d, want 1", pr.Owner)
	}
	if pr.VX >= 0 {
		t.Errorf("velocity %d, want it travelling left", pr.VX)
	}
	if pr.X >= s.Players[1].X {
		t.Errorf("spawned at %d, in front of a left-facing player at %d", pr.X, s.Players[1].X)
	}

	before := s.Projectiles[0].X
	s.Advance([2]uint16{0, 0})
	if s.Projectiles[0].X >= before {
		t.Error("a left-facing fireball travelled right")
	}
}

// It outlives its move: the character has recovered and is standing idle long
// before the fireball reaches the other side.
func TestFireballOutlivesItsMove(t *testing.T) {
	s := fireball(t)
	// Out of the way and behind the shooter, so the fireball dies of old age
	// rather than of hitting someone.
	s.Players[1].X = FromInt(-190)

	for range char().Moves[2].Total() {
		s.Advance([2]uint16{0, 0})
	}
	if s.Players[0].State == StateAttack {
		t.Fatal("the move should be over by now")
	}
	if live(&s) != 1 {
		t.Error("the fireball died with the move that fired it")
	}
}

// Life and the stage bound both end it, and it is gone rather than sitting in
// the corner as an invisible hitbox.
func TestFireballExpires(t *testing.T) {
	s := fireball(t)
	s.Players[1].X = FromInt(-190) // nothing in its path

	for range char().Moves[2].Proj.Life + 1 {
		if live(&s) == 0 {
			return
		}
		s.Advance([2]uint16{0, 0})
	}
	t.Errorf("still alive after its whole life, X = %d", s.Projectiles[0].X)
}

func TestFireballHitsOnceAndIsSpent(t *testing.T) {
	s := fireball(t)
	mv := &char().Moves[2]
	full := s.Players[1].Health

	for range 200 {
		s.Advance([2]uint16{0, 0})
		if s.Players[1].Health != full {
			break
		}
	}

	if got := full - s.Players[1].Health; got != mv.Damage {
		t.Errorf("dealt %d damage, want %d", got, mv.Damage)
	}
	if s.Players[1].State != StateHitstun {
		t.Errorf("defender state %d, want hitstun", s.Players[1].State)
	}
	if live(&s) != 0 {
		t.Error("the fireball survived its own hit")
	}

	after := s.Players[1].Health
	for range 60 {
		s.Advance([2]uint16{0, 0})
	}
	if s.Players[1].Health != after {
		t.Error("something hit a second time after the projectile was spent")
	}
}

// A projectile connects through applyHit, so blocking works exactly as it does
// for a normal — and blocking spends it too.
func TestFireballCanBeBlocked(t *testing.T) {
	s := fireball(t)
	full := s.Players[1].Health

	// Player 1 faces left, so holding right is holding back.
	for range 200 {
		s.Advance([2]uint16{0, InRight})
		if s.Players[1].State == StateBlockstun {
			break
		}
	}

	if s.Players[1].State != StateBlockstun {
		t.Fatalf("defender state %d, want blockstun", s.Players[1].State)
	}
	if s.Players[1].Health != full {
		t.Errorf("blocking cost %d health", full-s.Players[1].Health)
	}
	if live(&s) != 0 {
		t.Error("a blocked fireball was not spent")
	}
}

// The owner's own move is not marked as having hit: the fireball's owner may be
// mid-punch by the time it lands, and that punch must keep its hitbox.
func TestAProjectileHitDoesNotSpendTheOwnersMove(t *testing.T) {
	s := fireball(t)
	full := s.Players[1].Health

	for range 200 {
		s.Advance([2]uint16{0, 0})
		if s.Players[1].Health != full {
			break
		}
	}
	if s.Players[0].HasHit != 0 {
		t.Error("the projectile marked its owner's move as having connected")
	}
}

// A full pool drops the new one. Evicting a live fireball instead would let a
// player's own input delete a projectile already on screen.
func TestPoolIsBounded(t *testing.T) {
	s := New()
	mv := &char().Moves[2]

	for range MaxProjectiles + 2 {
		s.spawn(0, 2, mv)
	}
	if live(&s) != MaxProjectiles {
		t.Errorf("%d live, want the pool size %d", live(&s), MaxProjectiles)
	}
}

// Projectiles are state, so they roll back. This is the test that fails if the
// pool is ever moved out of GameState for tidiness.
func TestProjectilesRollBack(t *testing.T) {
	sess := NewSession()
	for _, in := range [][2]uint16{{pad[2], 0}, {pad[3], 0}, {pad[6] | InLP, 0}} {
		sess.Advance(in)
	}
	for sess.State().Players[0].StateFrame < char().Moves[2].Startup {
		sess.Advance([2]uint16{0, 0})
	}

	spawnFrame := sess.Frame()
	at := sess.State().Projectiles[0]
	if at.Active == 0 {
		t.Fatal("no projectile to roll back")
	}

	for range 5 {
		sess.Advance([2]uint16{0, 0})
	}
	if sess.State().Projectiles[0].X == at.X {
		t.Fatal("the projectile did not move, so the rewind proves nothing")
	}

	if !sess.Rewind(spawnFrame) {
		t.Fatal("rewind refused")
	}
	if got := sess.State().Projectiles[0]; got != at {
		t.Errorf("after the rewind: %+v, want %+v", got, at)
	}
}

// Point blank, a projectile is born inside the defender and resolves on its
// spawn frame — it is never alive at the end of a frame, and the view never
// draws it. That is correct, and it is also the case a playtest found.
//
// What it pins down is the frame after: the hit's hitstop parks the state
// machine on the spawn frame for several frames, and the move must not fire a
// second projectile on each of them. Nothing enforces that but the order of
// Advance, which returns during hitstop before advanceProjectiles can run —
// so this test is what will notice if that order ever changes.
func TestHitstopOnTheSpawnFrameCannotRespawn(t *testing.T) {
	s := New()
	s.Players[1].X = FromInt(-30) // inside where the fireball appears

	feed(&s, 2, 3)
	s.Advance([2]uint16{pad[6] | InLP, 0})
	if s.Players[0].MoveIndex != 2 {
		t.Fatalf("the fireball did not come out: move %d", s.Players[0].MoveIndex)
	}

	mv := &char().Moves[2]
	full := s.Players[1].Health

	for range 30 {
		s.Advance([2]uint16{0, 0})
		if s.Players[1].Health != full {
			break
		}
	}

	if got := full - s.Players[1].Health; got != mv.Damage {
		t.Fatalf("dealt %d damage, want one hit of %d", got, mv.Damage)
	}
	if s.Hitstop == 0 {
		t.Fatal("no hitstop, so this is not the case being tested")
	}
	if s.Players[0].StateFrame != mv.Startup {
		t.Fatalf("state frame %d, want it parked on the spawn frame %d",
			s.Players[0].StateFrame, mv.Startup)
	}
	if live(&s) != 0 {
		t.Fatal("the fireball survived a hit it landed on its own spawn frame")
	}

	// Through the hitstop and out the far side of the move: one input, one
	// projectile, one hit.
	after := s.Players[1].Health
	for range mv.Total() + 30 {
		s.Advance([2]uint16{0, 0})
		if n := live(&s); n != 0 {
			t.Fatalf("frame %d: %d projectile(s) appeared from the same input", s.Frame, n)
		}
	}
	if s.Players[1].Health != after {
		t.Errorf("health moved again: %d then %d", after, s.Players[1].Health)
	}
}
