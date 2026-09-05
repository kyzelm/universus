package sim

import "testing"

// The fixture's sweep and the input that selects it. Crouch plus HK: standing
// HK is the short launcher on move 4, so the stance is doing real work here.
const (
	sweepIndex = int32(11)
	sweepInput = InDown | InHK
)

// sweep lands the fixture's knockdown strike on player 1 and returns the state
// on the frame it connected. Player 1 holds nothing, so the low is not blocked.
func sweep(t *testing.T) GameState {
	t.Helper()

	s := facing(30)
	s.Advance([2]uint16{sweepInput, 0})
	if s.Players[0].MoveIndex != sweepIndex {
		t.Fatalf("crouching HK gave move %d, want the sweep %d", s.Players[0].MoveIndex, sweepIndex)
	}

	for range char().Moves[sweepIndex].Total() {
		if s.Players[1].State == StateHitstun {
			return s
		}
		s.Advance([2]uint16{sweepInput, 0})
	}
	t.Fatal("the sweep never connected")
	return s
}

// knockedDown runs the sweep through its hitstun and returns the state on the
// first frame player 1 is on the floor.
func knockedDown(t *testing.T) GameState {
	t.Helper()

	s := sweep(t)
	for range 200 {
		s.Advance([2]uint16{0, 0})
		if s.Players[1].State != StateHitstun {
			break
		}
	}
	if got := s.Players[1].State; got != StateKnockdown {
		t.Fatalf("player 1 is in state %d after the sweep's hitstun, want the knockdown", got)
	}
	return s
}

// The knockdown is owed at the *end* of the hitstun, not instead of it: the
// defender is struck, held for the move's frames, and only then goes down.
func TestASweepKnocksDownWhenItsHitstunEnds(t *testing.T) {
	s := sweep(t)
	if s.Players[1].State != StateHitstun {
		t.Fatalf("player 1 is in state %d on the connect, want hitstun", s.Players[1].State)
	}
	if s.Players[1].Down == 0 {
		t.Fatal("the sweep connected but owes no knockdown")
	}

	s = knockedDown(t)
	if got := s.Players[1].Stun; got != balance.KnockdownFrames {
		t.Errorf("knocked down for %d frames, want the balance value %d", got, balance.KnockdownFrames)
	}
}

// A move that does not knock down does not, which is the half that makes the
// flag mean anything: the jab is the same code path with the flag off.
func TestAPlainHitDoesNotKnockDown(t *testing.T) {
	s := facing(30)
	for range 40 {
		s.Advance([2]uint16{InLP, 0})
		if s.Players[1].Down != 0 {
			t.Fatal("the jab owes a knockdown")
		}
		if s.Players[1].State == StateKnockdown {
			t.Fatal("the jab knocked the defender down")
		}
	}
}

// **Invulnerable for the whole knockdown, and actionable on the frame the
// invulnerability ends.** Any overlap the other way is a window in which the
// defender can be hit and cannot block, and an attack timed into it knocks them
// down again — a loop with no escape. One test, because the two halves are the
// same boundary read from either side.
func TestVulnerableAndActionableBeginTogether(t *testing.T) {
	s := knockedDown(t)

	var boxes [MaxBoxes]Box
	for range balance.KnockdownFrames + 10 {
		if s.Players[1].State != StateKnockdown {
			break
		}
		if n := s.Hurtboxes(1, &boxes); n != 0 {
			t.Fatalf("frame %d of the knockdown has %d hurtboxes, want none", s.Players[1].StateFrame, n)
		}
		if s.throwable(1) {
			t.Fatalf("frame %d of the knockdown is throwable", s.Players[1].StateFrame)
		}
		s.Advance([2]uint16{0, 0})
	}

	if got := s.Players[1].State; !Actionable(got) {
		t.Fatalf("player 1 is in state %d off the floor, want an actionable one", got)
	}
	if n := s.Hurtboxes(1, &boxes); n == 0 {
		t.Error("player 1 is actionable and still has no hurtboxes")
	}
}

// The rise is what the input buffer is for: a reversal pressed on the floor
// comes out on the first actionable frame, which is the design's "options on
// rising" and costs no code of its own.
func TestAReversalBufferedOnTheFloorComesOutOnTheRise(t *testing.T) {
	const reversal = int32(7)

	s := knockedDown(t)
	for range balance.KnockdownFrames + InputBuffer {
		in := uint16(0)
		// One press, late enough to still be live when they rise. A held button
		// is not a fresh press, so this is one input and not many.
		if s.Players[1].State == StateKnockdown && s.Players[1].Stun == 2 {
			in = InMP
		}
		s.Advance([2]uint16{0, in})
		if s.Players[1].MoveIndex == reversal {
			return
		}
	}
	t.Errorf("player 1 is in state %d with move %d, want the buffered reversal",
		s.Players[1].State, s.Players[1].MoveIndex)
}

// A throw ends on the floor too — the fixed count of helpless frames a thrown
// player used to spend is now the throw's own animation, with the same
// knockdown as a sweep after it.
func TestAThrowEndsOnTheFloor(t *testing.T) {
	s := throwUntil(t, 30, 0, resolved)
	if s.Players[1].State != StateThrown {
		t.Fatalf("player 1 is in state %d, want the throw", s.Players[1].State)
	}
	if s.Players[1].Down == 0 {
		t.Fatal("the throw owes no knockdown")
	}

	for range 200 {
		s.Advance([2]uint16{0, 0})
		if s.Players[1].State != StateThrown {
			break
		}
	}
	if got := s.Players[1].State; got != StateKnockdown {
		t.Errorf("player 1 is in state %d after the throw, want the knockdown", got)
	}
}

// A tech does not. It is symmetric, and a knockdown on either side of a
// symmetric escape is an advantage the mechanic exists not to give.
func TestATechKnocksNobodyDown(t *testing.T) {
	mv := &char().Moves[throwIndex]

	s := facing(30)
	s.Advance([2]uint16{throwInput, 0})
	for s.Players[1].State != StateThrown && s.Players[0].State == StateAttack {
		in := uint16(0)
		if s.Players[0].StateFrame == mv.Startup-2 {
			in = throwInput
		}
		s.Advance([2]uint16{0, in})
	}

	for range balance.ThrowTechRecovery + balance.KnockdownFrames {
		s.Advance([2]uint16{0, 0})
		for i, p := range s.Players {
			if p.State == StateKnockdown {
				t.Fatalf("player %d was knocked down by a tech", i)
			}
		}
	}
}

// Being hit out of the hitstun replaces the old hit's consequences with the new
// one's rather than stacking them: a swept player who is picked up by a second
// hit is no longer on their way to the floor.
//
// Poked directly rather than staged as a real second hit. What is under test is
// that entering a state clears the debt, and a two-hit sequence tuned to land
// inside a 12-frame hitstun would fail the day anyone touches the fixture's
// frame data, for a reason that has nothing to do with knockdowns.
func TestANewHitCancelsTheOwedKnockdown(t *testing.T) {
	s := sweep(t)
	p := &s.Players[1]
	if p.Down == 0 {
		t.Fatal("the sweep owes no knockdown")
	}

	p.enter(StateHitstun)
	if p.Down != 0 {
		t.Error("the knockdown survived a new hit")
	}
}
