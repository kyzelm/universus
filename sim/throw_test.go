package sim

import "testing"

// The fixture's throw and its buttons. Named because every test below presses
// them, and `InLP|InLK` at each call site reads like a coincidence rather than
// like one input.
const (
	throwIndex = int32(10)
	throwInput = InLP | InLK
)

// away is what player 1 holds to block: they start facing left, so holding
// right is holding away from the opponent.
const away = InRight

// throwUntil starts player 0's throw and advances until stop says to halt or
// the move is over, giving player 1 defenderIn every frame. It returns the
// frame of the throw the state stopped on.
//
// The loop exists because a throw connects on its own active frames rather than
// on a frame number the caller can count to, and every test below cares about
// what was true at exactly that moment.
func throwUntil(t *testing.T, gap int, defenderIn uint16, stop func(s *GameState) bool) GameState {
	t.Helper()

	s := facing(gap)
	s.Advance([2]uint16{throwInput, defenderIn})
	if s.Players[0].MoveIndex != throwIndex {
		t.Fatalf("LP+LK gave move %d, want the throw %d", s.Players[0].MoveIndex, throwIndex)
	}

	for range char().Moves[throwIndex].Total() {
		if stop(&s) {
			break
		}
		s.Advance([2]uint16{0, defenderIn})
	}
	return s
}

// resolved stops once the throw has connected one way or the other, or run out.
func resolved(s *GameState) bool {
	return s.Players[1].State == StateThrown || s.Players[0].State != StateAttack
}

// Two buttons, and both of them. LP alone is the jab — the whole point of the
// throw's tier is that the more specific input wins, and the only way to see
// that is to press the less specific one too.
func TestThrowNeedsBothButtons(t *testing.T) {
	jab := New()
	jab.Advance([2]uint16{InLP, 0})
	if got := jab.Players[0].MoveIndex; got != 0 {
		t.Errorf("LP alone gave move %d, want the jab", got)
	}

	both := New()
	both.Advance([2]uint16{throwInput, 0})
	if got := both.Players[0].MoveIndex; got != throwIndex {
		t.Errorf("LP+LK gave move %d, want the throw", got)
	}
}

// **Both buttons on one frame, from an actionable state.** Pressing LP and then
// LK a frame later gives the jab and no throw, which is what the genre does and
// what one press, one move already requires: the jab came out on the frame it
// was asked for, and nothing retroactively turns it into something else.
//
// The pair completing *late* still counts when the player could not act on the
// first button — which is the case the mask rule is really for.
func TestBothButtonsMustLandOnOneFrame(t *testing.T) {
	late := New()
	late.Advance([2]uint16{InLP, 0}) // the jab comes out here
	late.Advance([2]uint16{throwInput, 0})
	if got := late.Players[0].MoveIndex; got != 0 {
		t.Errorf("LP then LK gave move %d, want the jab that already started", got)
	}

	// Now the same input while the jab is still running: neither button could
	// be acted on when it landed, so the pair completes inside the buffer and
	// the throw is what comes out of it.
	buffered := New()
	jab := &char().Moves[0]
	buffered.Advance([2]uint16{InLP, 0})
	for buffered.Players[0].StateFrame < jab.Total()-2 {
		buffered.Advance([2]uint16{0, 0})
	}
	buffered.Advance([2]uint16{InLP, 0})
	buffered.Advance([2]uint16{throwInput, 0})

	for range InputBuffer {
		buffered.Advance([2]uint16{0, 0})
		if buffered.Players[0].MoveIndex == throwIndex {
			return
		}
	}
	t.Errorf("the buffered throw never came out: move %d", buffered.Players[0].MoveIndex)
}

// A throw beats blocking. That is the entire reason it exists: without it the
// correct play against every strike in the game is to hold back and wait.
func TestAThrowBeatsBlocking(t *testing.T) {
	s := throwUntil(t, 30, away, resolved)

	dp := &s.Players[1]
	if dp.State == StateBlockstun {
		t.Fatal("the throw produced blockstun, which is a throw that can be blocked")
	}
	if dp.State != StateThrown {
		t.Fatalf("state %d, want thrown", dp.State)
	}
	if dp.Health >= CharacterAt(dp.Char).Health {
		t.Errorf("health %d unchanged: the throw dealt nothing", dp.Health)
	}
	if s.Players[0].HasHit == 0 {
		t.Error("the throw did not mark its own connect")
	}
}

// …and loses to jumping, which is what makes the pre-jump frames being grounded
// a rule rather than an animation detail: the throw beats the *attempt*.
func TestAThrowMissesTheAirborneAndCatchesPreJump(t *testing.T) {
	airborne := facing(30)
	hold(&airborne, int(char().PreJumpFrames)+1, InUp)
	if !airborne.Players[0].Airborne() {
		t.Fatal("setup: player 0 never left the ground")
	}
	// Player 1 throws at someone already in the air.
	airborne.Advance([2]uint16{0, throwInput})
	for range char().Moves[throwIndex].Total() {
		airborne.Advance([2]uint16{0, 0})
		if airborne.Players[0].State == StateThrown {
			t.Fatal("an airborne player was thrown")
		}
	}

	// The jump attempt: the up press lands late enough that the throw's active
	// frames arrive while the defender is still in pre-jump, which is grounded.
	mv := &char().Moves[throwIndex]
	early := facing(30)
	early.Advance([2]uint16{throwInput, 0})

	var inPreJump bool
	for early.Players[0].State == StateAttack {
		in := uint16(0)
		if early.Players[0].StateFrame == mv.Startup-2 {
			in = InUp
		}
		early.Advance([2]uint16{0, in})
		inPreJump = inPreJump || early.Players[1].State == StatePreJump
		if early.Players[1].State == StateThrown {
			break
		}
	}

	if !inPreJump {
		t.Fatal("setup: the defender was never in pre-jump while the throw was active")
	}
	if early.Players[1].State != StateThrown {
		t.Errorf("state %d out of pre-jump, want thrown: throws must beat jump attempts",
			early.Players[1].State)
	}
}

// Stun grants throw immunity, in every state that is one. Without it a blocked
// light leads to a throw the defender cannot contest, because they are still in
// stun at the moment it lands — pressure with no answer in it.
//
// The states are set directly: what is under test is the rule, and building a
// real blockstun of the right length at the right distance would be a test of
// the jab's frame data instead.
func TestSomeoneInStunCannotBeThrown(t *testing.T) {
	mv := &char().Moves[throwIndex]

	for _, tc := range []struct {
		name  string
		state int32
	}{
		{"hitstun", StateHitstun},
		{"blockstun", StateBlockstun},
		{"landing recovery", StateLanding},
		{"already being thrown", StateThrown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := facing(30)
			s.Advance([2]uint16{throwInput, 0})

			s.Players[1].enter(tc.state)
			s.Players[1].Stun = mv.Total()

			for range mv.Startup + mv.Active {
				s.Advance([2]uint16{0, 0})
			}

			if s.Players[1].State == StateThrown && tc.state != StateThrown {
				t.Error("a player in stun was thrown")
			}
			if s.Players[1].Health != char().Health {
				t.Errorf("health %d: the throw took someone it cannot take", s.Players[1].Health)
			}
			if s.Players[0].HasHit != 0 {
				t.Error("the throw counted as a connect against a defender it cannot take")
			}
		})
	}
}

// A throw that whiffs has to actually whiff: its recovery is what makes going
// for one a risk rather than a free guess.
func TestAWhiffedThrowRunsItsRecovery(t *testing.T) {
	mv := &char().Moves[throwIndex]
	s := throwUntil(t, 300, 0, func(s *GameState) bool {
		return s.Players[0].StateFrame >= mv.Startup+mv.Active
	})

	if s.Players[0].HasHit != 0 {
		t.Error("a throw at 300 units connected")
	}
	if p := s.Players[0]; p.State != StateAttack || p.StateFrame >= mv.Total() {
		t.Errorf("state %d frame %d, want the throw still in its own recovery",
			p.State, p.StateFrame)
	}
}

// The tech: press throw as it lands and nobody is thrown. Symmetric on purpose
// — contesting a throw resets the situation rather than winning it.
func TestATechEscapesTheThrow(t *testing.T) {
	mv := &char().Moves[throwIndex]
	full := char().Health

	s := facing(30)
	gap := s.Players[1].X - s.Players[0].X
	s.Advance([2]uint16{throwInput, 0})

	// The defender's own throw press, two frames before the throw connects:
	// inside the window, and a reaction rather than a frame-perfect answer.
	for s.Players[1].State != StateThrown && s.Players[0].State == StateAttack {
		in := uint16(0)
		if s.Players[0].StateFrame == mv.Startup-2 {
			in = throwInput
		}
		s.Advance([2]uint16{0, in})
	}

	if s.Players[1].Health != full {
		t.Errorf("health %d after a tech, want %d: a teched throw deals no damage",
			s.Players[1].Health, full)
	}
	for i, p := range s.Players {
		if p.State != StateThrown {
			t.Errorf("player %d is in state %d, want both in the tech recovery", i, p.State)
		}
	}
	if a, b := s.Players[0].Stun, s.Players[1].Stun; a != b {
		t.Errorf("recovery %d against %d: a tech leaves neither player at an advantage", a, b)
	}
	if now := s.Players[1].X - s.Players[0].X; now <= gap {
		t.Errorf("the players are %d apart, were %d: a tech pushes them apart", now, gap)
	}
}

// The window is the balance value and not a number this file invented — and it
// is the boundary that says a tech is a reaction rather than a state. Tested
// directly, because arranging a stale press end to end means the defender
// pressing throw early enough to start a throw of their own, which changes what
// is being measured. The other half — no press at all, and the throw lands —
// is TestAThrowBeatsBlocking above.
func TestTheTechWindowIsTheBalanceValue(t *testing.T) {
	s := New()
	s.Advance([2]uint16{0, throwInput})

	if got := s.techFrame(1, s.latest()); got < 0 {
		t.Error("a throw press was not seen on the frame after it")
	}
	hold(&s, int(BalanceOf().ThrowTechFrames), 0)
	if got := s.techFrame(1, s.latest()); got >= 0 {
		t.Errorf("a press %d frames old still techs, and the window is %d",
			BalanceOf().ThrowTechFrames+1, BalanceOf().ThrowTechFrames)
	}
}
