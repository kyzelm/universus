package sim

import "testing"

// The fixture's anti-air launcher and the button that selects it.
const (
	launcherIndex = int32(12)
	launcherInput = InLK
)

// strike presses p0 until the move it selects lands on player 1, and returns
// the state on the frame of the connect. p1 is what the defender holds, which
// is what decides whether they block.
func strike(t *testing.T, gap int, p0, p1 uint16) GameState {
	t.Helper()

	s := facing(gap)
	for range 60 {
		s.Advance([2]uint16{p0, p1})
		switch s.Players[1].State {
		case StateHitstun, StateBlockstun:
			return s
		}
	}
	t.Fatal("the move never connected")
	return GameState{}
}

// pushStep advances until player 1's position changes, and returns the state and
// how far they moved. The hit that precedes it freezes the match for its
// hitstop frames, so the push does not show up on the frame it was given.
func pushStep(t *testing.T, s *GameState) Fix {
	t.Helper()

	for range 30 {
		before := s.Players[1].X
		s.Advance([2]uint16{0, 0})
		if d := s.Players[1].X - before; d != 0 {
			return d
		}
	}
	t.Fatal("the defender never moved")
	return 0
}

// **A hit moves the defender.** Before knockback existed nothing a connect did
// touched anyone's position, so a blockstring never spaced itself out — the
// first frame of the push is the move's own number, and friction takes it from
// there rather than out of the impulse.
func TestAHitPushesTheDefenderAway(t *testing.T) {
	s := strike(t, 40, InLP, 0)

	if got := pushStep(t, &s); got != balance.KnockbackHit {
		t.Errorf("first frame of pushback moved %v, want the balance value %v", got, balance.KnockbackHit)
	}
	// Half of it left the next frame: the fixture keeps 50%.
	if got, want := pushStep(t, &s), balance.KnockbackHit/2; got != want {
		t.Errorf("second frame moved %v, want %v", got, want)
	}
}

// Blocking has a pushback number of its own and it is the larger one: it is
// what spaces a blockstring out, and therefore what decides whether pressure
// continues or ends.
func TestBlockPushbackIsItsOwnNumber(t *testing.T) {
	s := strike(t, 40, InLP, InRight)
	if s.Players[1].State != StateBlockstun {
		t.Fatalf("player 1 is in state %d, want blockstun", s.Players[1].State)
	}

	if got := pushStep(t, &s); got != balance.KnockbackBlock {
		t.Errorf("blocked push moved %v, want %v", got, balance.KnockbackBlock)
	}
	if balance.KnockbackBlock <= balance.KnockbackHit {
		t.Error("the fixture's block push is not larger than its hit push, so this test proves nothing")
	}
}

// Away is decided by the two positions, not by the attacker's facing: at the
// instant of a crossup the two disagree, and pushing by facing would pull the
// defender through the attacker on exactly the hit where it matters.
func TestPushbackIsAwayFromTheAttacker(t *testing.T) {
	s := facing(-40) // player 0 on the right this time
	s.Players[0].Facing, s.Players[1].Facing = -1, 1

	for range 60 {
		s.Advance([2]uint16{InLP, 0})
		if s.Players[1].State == StateHitstun {
			break
		}
	}
	if s.Players[1].State != StateHitstun {
		t.Fatal("the jab never connected")
	}

	before := s.Players[1].X
	for range 30 {
		s.Advance([2]uint16{0, 0})
		if s.Players[1].X != before {
			break
		}
	}
	if s.Players[1].X >= before {
		t.Errorf("defender on the left moved to %v from %v, want pushed further left", s.Players[1].X, before)
	}
}

// **The corner rule**, and the reason pushback is worth having at all: the wall
// does not move, so the push the clamp would have eaten moves the attacker back
// instead. Without it corner pressure — a core mechanic, not a side effect —
// would be the one place pushback silently did nothing.
func TestTheCornerPushesTheAttackerBack(t *testing.T) {
	s := facing(40)
	// Player 1's pushbox is 12 wide either side, so this is exactly the wall.
	s.Players[1].X = StageHalfWidth - FromInt(12)
	s.Players[0].X = s.Players[1].X - FromInt(40)

	for range 60 {
		s.Advance([2]uint16{InLP, 0})
		if s.Players[1].State == StateHitstun {
			break
		}
	}
	if s.Players[1].State != StateHitstun {
		t.Fatal("the jab never connected")
	}

	wall, attacker := s.Players[1].X, s.Players[0].X
	for range 30 {
		s.Advance([2]uint16{0, 0})
		if s.Players[0].X != attacker {
			break
		}
	}

	if s.Players[1].X != wall {
		t.Errorf("the cornered defender moved to %v from %v, but the wall is behind them", s.Players[1].X, wall)
	}
	if got, want := attacker-s.Players[0].X, balance.KnockbackHit; got != want {
		t.Errorf("the attacker was pushed back %v, want the whole eaten push %v", got, want)
	}
}

// A launcher is a knockback with a vertical component and nothing else — no
// second field, no flag that could disagree with the velocity.
func TestALauncherPutsTheDefenderInTheAir(t *testing.T) {
	s := strike(t, 40, launcherInput, 0)
	if s.Players[0].MoveIndex != launcherIndex {
		t.Fatalf("standing LK gave move %d, want the launcher %d", s.Players[0].MoveIndex, launcherIndex)
	}

	if got, want := s.Players[1].VY, char().Moves[launcherIndex].KnockbackVY; got != want {
		t.Fatalf("the defender's vertical velocity is %v, want the move's %v", got, want)
	}

	airborne := false
	for range 20 {
		s.Advance([2]uint16{0, 0})
		if s.Players[1].Airborne() {
			airborne = true
			break
		}
	}
	if !airborne {
		t.Fatal("the launcher left the defender on the ground")
	}
}

// **Hitstun does not run out in mid-air, and a juggle ends on the floor.** The
// alternative is an opponent who becomes actionable in the air at the end of a
// combo, which was reachable by any air-to-air hit before launchers existed.
func TestAJuggleEndsInAKnockdown(t *testing.T) {
	s := strike(t, 40, launcherInput, 0)
	stun := char().Moves[launcherIndex].Hitstun

	for range 200 {
		s.Advance([2]uint16{0, 0})
		if s.Players[1].State == StateKnockdown {
			break
		}
		if s.Players[1].Airborne() && s.Players[1].State != StateHitstun {
			t.Fatalf("the launched defender is in state %d in mid-air, want hitstun",
				s.Players[1].State)
		}
		// Well past the move's own hitstun, and still helpless: what ends it is
		// the floor, not the counter.
		if s.Frame > uint32(stun)*3 && s.Players[1].State != StateKnockdown && !s.Players[1].Airborne() {
			t.Fatal("the defender came down without being knocked down")
		}
	}

	if got := s.Players[1].State; got != StateKnockdown {
		t.Fatalf("the defender is in state %d after landing, want the knockdown", got)
	}
	if got := s.Players[1].Juggle; got != 0 {
		t.Errorf("the juggle counter is %d on landing, want it reset", got)
	}
}

// The juggle counter counts hits taken in the air, and the launcher is not one
// of them: it is what put the defender up there, and counting it would spend a
// juggle on the hit that opened the combo.
func TestJuggleHitsCountOnlyInTheAir(t *testing.T) {
	s := strike(t, 40, launcherInput, 0)
	if got := s.Players[1].Juggle; got != 0 {
		t.Fatalf("the launcher counted itself as juggle hit %d", got)
	}

	var maxJuggle int32
	for range 300 {
		in := uint16(0)
		if Actionable(s.Players[0].State) {
			in = launcherInput
		}
		s.Advance([2]uint16{in, 0})

		if j := s.Players[1].Juggle; j > maxJuggle {
			maxJuggle = j
		}
		if s.Players[1].State == StateKnockdown {
			break
		}
	}

	if maxJuggle == 0 {
		t.Error("no hit landed in the air, so nothing here was counted")
	}
	if maxJuggle > balance.JuggleLimit {
		t.Errorf("the defender took %d juggle hits, past the limit of %d", maxJuggle, balance.JuggleLimit)
	}
	if s.Players[1].State != StateKnockdown {
		t.Fatal("the juggle never ended on the floor")
	}
}

// airborne puts player 1 in the air with n juggle hits already on them, still
// rising so they are still up there when the follow-up lands. Set up rather
// than played out: reaching an exact juggle count by playing is a timing test
// wearing a limit test's name.
func airborne(n int32) GameState {
	s := facing(40)
	s.Players[1].enter(StateAir)
	s.Players[1].Y = FromInt(10)
	s.Players[1].VY = FromInt(4)
	s.Players[1].Juggle = n
	return s
}

// **The limit refuses the connect.** It has to be a whiff and not a softened
// hit: the combo ends because the move misses, which is also what gives the
// defender the frames the miss costs.
func TestAMoveRefusesToConnectAtTheJuggleLimit(t *testing.T) {
	for _, tc := range []struct {
		name string
		n    int32
		want bool
	}{
		{"one hit below the limit", balance.JuggleLimit - 1, true},
		{"at the limit", balance.JuggleLimit, false},
		{"past it", balance.JuggleLimit + 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := airborne(tc.n)
			hit := false
			for range 20 {
				s.Advance([2]uint16{launcherInput, 0})
				if s.Players[1].State == StateHitstun {
					hit = true
					break
				}
			}
			if hit != tc.want {
				t.Errorf("the follow-up connected: %v, want %v", hit, tc.want)
			}
		})
	}
}

// Gravity rises with every juggle hit, so the opponent falls faster and the
// combo self-terminates rather than merely hitting a cap.
func TestGravityRisesWithEachJuggleHit(t *testing.T) {
	p := PlayerState{Char: 0}
	base := char().Gravity

	if got := p.gravity(); got != base {
		t.Errorf("gravity with no juggles is %v, want the character's own %v", got, base)
	}

	p.Juggle = 1
	want := base + base.Mul(FromInt(int(balance.JuggleGravityPercent))).Div(FromInt(100))
	if got := p.gravity(); got != want {
		t.Errorf("gravity after one juggle hit is %v, want %v", got, want)
	}
	// Heavier, and gravity is negative, so heavier is smaller.
	if p.gravity() >= base {
		t.Errorf("gravity after a juggle hit is %v, no heavier than %v", p.gravity(), base)
	}
}

// The default is the balance file's, so the two dozen moves that want the
// ordinary push do not each author it — and each get a chance to author it
// differently by mistake.
func TestKnockbackFallsBackToTheBalanceDefault(t *testing.T) {
	var plain Move

	vx, vy := plain.Knockback(false)
	if vx != balance.KnockbackHit || vy != 0 {
		t.Errorf("an unauthored hit push is (%v, %v), want (%v, 0)", vx, vy, balance.KnockbackHit)
	}
	vx, vy = plain.Knockback(true)
	if vx != balance.KnockbackBlock || vy != 0 {
		t.Errorf("an unauthored block push is (%v, %v), want (%v, 0)", vx, vy, balance.KnockbackBlock)
	}

	// A blocked launcher does not launch: blocking is what stops it.
	launcher := char().Moves[launcherIndex]
	if _, vy := launcher.Knockback(true); vy != 0 {
		t.Errorf("a blocked launcher gives %v of vertical push, want none", vy)
	}
	if !launcher.Launcher() {
		t.Error("the fixture's launcher does not report itself as one")
	}
	if plain.Launcher() {
		t.Error("a move with the default push reports itself a launcher")
	}
}
