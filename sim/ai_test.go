package sim

import "testing"

// The rule tests run with the imperfection roll switched off, because it exists
// to make the AI *not* take the rule it found: with it on, "anti-airs a jump-in"
// is a statement about a percentage rather than about the rule list. The roll
// has a test of its own below.
func certain(t *testing.T) {
	t.Helper()
	b := testBalance()
	for i := range b.AITiers {
		b.AITiers[i].RandomPercent = 0
		b.AITiers[i].BlockPercent = 100
	}
	LoadBalance(b)
	t.Cleanup(func() { LoadBalance(testBalance()) })
}

// A seat nobody is driving presses nothing, and the input passed for it is
// still the input it gets. The AI is opt-in per seat or it is a bug in every
// match ever played.
func TestAHumanSeatIsUntouched(t *testing.T) {
	s := New()
	if got := s.aiInput(0); got != 0 {
		t.Errorf("a human seat pressed %#b", got)
	}

	s.Advance([2]uint16{InLP, 0})
	if got := s.Players[0].State; got != StateAttack {
		t.Errorf("player 1 is in state %d, want the jab the caller pressed", got)
	}
}

// **The property the whole design rests on**: the AI decides inside the sim,
// from state, with the state's own generator — so the same match setup plays
// out identically every time, and an AI match is a replayable one.
func TestAnAIMatchIsReproducible(t *testing.T) {
	play := func() uint32 {
		s := NewAIMatch(AIHard)
		s.Players[0].AI = AINormal
		for f := 0; f < 2000; f++ {
			s.Advance([2]uint16{0, 0})
		}
		return s.Checksum()
	}
	if a, b := play(), play(); a != b {
		t.Errorf("two identical AI matches ended at %#x and %#x", a, b)
	}
}

// A rollback replays the AI's decisions rather than re-rolling them: its plan
// and the generator it drew from are both in the state, so rewinding takes them
// back together. This is the same test the rollback gate runs on human input,
// pointed at the one input source that is generated inside the sim.
func TestAIDecisionsRollBack(t *testing.T) {
	s := NewAIMatch(AIHard)
	s.Players[0].AI = AIEasy

	for f := 0; f < 300; f++ {
		s.Advance([2]uint16{0, 0})
	}
	saved := s
	for f := 0; f < 60; f++ {
		s.Advance([2]uint16{0, 0})
	}
	want := s.Checksum()

	s = saved
	for f := 0; f < 60; f++ {
		s.Advance([2]uint16{0, 0})
	}
	if got := s.Checksum(); got != want {
		t.Errorf("replayed to %#x, want %#x — an AI decision did not roll back", got, want)
	}
}

// Non-degeneracy, the design note's own bar: two of these play for 10 000
// frames without deadlocking, without stalling in a corner, and without failing
// to end a round.
func TestAIvsAIDoesNotDegenerate(t *testing.T) {
	s := NewAIMatch(AINormal)
	s.Players[0].AI = AINormal

	hits := false
	start := [2]int32{s.Players[0].Health, s.Players[1].Health}
	near, far := false, false

	for f := 0; f < 10000; f++ {
		s.Advance([2]uint16{0, 0})
		if s.Players[0].Health < start[0] || s.Players[1].Health < start[1] {
			hits = true
		}
		// Two fighters that never close and never separate are deadlocked,
		// whatever their health bars say — a pair standing at opposite ends of
		// the stage for the whole round passes every other check here.
		if d := s.aiDistance(0); d <= balance.AICloseRange {
			near = true
		} else if d > balance.AIMidRange {
			far = true
		}
	}

	// Rounds, not the match: a best-of-three of timeouts is longer than this,
	// and it is ending *a round* that says the loop is not stuck.
	if s.Round < 2 && s.Winner == RoundNobody {
		t.Errorf("still on round %d after 10000 frames, wins %v", s.Round, s.Wins)
	}
	// A round decided entirely by the clock with nobody ever landing a hit is
	// two opponents standing still, which is exactly the degenerate case the
	// round check above would not notice.
	if !hits {
		t.Error("neither player took a hit in 10000 frames")
	}
	if !near || !far {
		t.Errorf("the gap never varied: closed %v, opened %v", near, far)
	}
}

// Anti-air is first in the priority list, and it is the rule most worth having:
// a jump-in beaten by a dragon punch is the exchange the whole list is built
// around.
func TestAIAntiAirsAJumpIn(t *testing.T) {
	certain(t)

	s := NewAIMatch(AIHard)
	s.Players[0].X = FromInt(-20)
	s.Players[1].X = FromInt(20)
	s.Players[0].State = StateAir
	s.Players[0].Y = FromInt(30)
	s.Players[0].StateFrame = 20 // long enough for a hard AI to have noticed

	if got := s.aiDecide(1); got != aiDP {
		t.Errorf("decided plan %d against a jump-in, want the dragon punch (%d)", got, aiDP)
	}
}

// **Reaction delay is the honest difficulty lever** (D63), so it has to
// actually delay something: an attack the AI has not had time to see is one it
// does not block, whatever its block percentage says.
func TestAIDoesNotReactBeforeItCouldHaveSeen(t *testing.T) {
	certain(t)

	attacked := func(age int32) int32 {
		s := NewAIMatch(AIHard)
		s.Players[0].X = FromInt(-20)
		s.Players[1].X = FromInt(20)
		s.Players[0].State = StateAttack
		s.Players[0].MoveIndex = 3 // the fixture's heavy: slow enough to react to
		s.Players[0].StateFrame = age
		return s.aiDecide(1)
	}

	reaction := testBalance().AITiers[aiTier(AIHard)].Reaction
	if got := attacked(reaction - 1); got == aiBlock {
		t.Errorf("blocked an attack %d frames old, inside its %d-frame reaction",
			reaction-1, reaction)
	}
	if got := attacked(reaction); got != aiBlock {
		t.Errorf("decided plan %d against an attack it has had time to see, want block", got)
	}
}

// The other lever, and the one that keeps the opponent from reading as a
// machine: some fraction of decisions throw the rule list away.
func TestTheImperfectionRollIgnoresTheRules(t *testing.T) {
	b := testBalance()
	for i := range b.AITiers {
		b.AITiers[i].RandomPercent = 100
	}
	LoadBalance(b)
	t.Cleanup(func() { LoadBalance(testBalance()) })

	// A jump-in overhead: the rule list has exactly one answer to this, so
	// anything else came from the roll.
	s := NewAIMatch(AIHard)
	s.Players[0].X = FromInt(-20)
	s.Players[1].X = FromInt(20)
	s.Players[0].State = StateAir
	s.Players[0].Y = FromInt(30)
	s.Players[0].StateFrame = 20

	other := 0
	for i := 0; i < 40; i++ {
		if s.aiDecide(1) != aiDP {
			other++
		}
	}
	if other == 0 {
		t.Error("every decision took the rule, with the imperfection roll at 100%")
	}
}

// Difficulty is the two levers and nothing else, so a harder tier must show up
// as behaviour: it blocks more of what it sees. Counted over many decisions,
// because one roll says nothing.
func TestHarderTiersBlockMoreOften(t *testing.T) {
	blocks := func(tier int32) int {
		s := NewAIMatch(tier)
		// Beyond close range on purpose: inside it, a recovering attack is a
		// punish and the block rule is never reached. The distance is what
		// separates the two rules, and this test is about the block one.
		s.Players[0].X = FromInt(-40)
		s.Players[1].X = FromInt(40)
		s.Players[0].State = StateAttack
		s.Players[0].MoveIndex = 3
		s.Players[0].StateFrame = 30 // seen by every tier

		n := 0
		for i := 0; i < 400; i++ {
			if s.aiDecide(1) == aiBlock {
				n++
			}
		}
		return n
	}

	easy, normal, hard := blocks(AIEasy), blocks(AINormal), blocks(AIHard)
	if !(easy < normal && normal < hard) {
		t.Errorf("blocks per 400 decisions: easy %d, normal %d, hard %d — not ordered",
			easy, normal, hard)
	}
}

// The dragon punch is pressed with the shortcut motion, and that is not a
// convenience: two of them in a row would otherwise spell a double
// quarter-circle across the gap between them, and the supers are scanned first
// (D78, D96). Read as directions, the AI's anti-air must contain no diagonal.
func TestTheAIsDragonPunchHasNoDiagonal(t *testing.T) {
	for age := int32(0); age < 3; age++ {
		bits := aiPress(aiDP, age, 1)
		if bits&InDown != 0 && (bits&InRight != 0 || bits&InLeft != 0) {
			t.Errorf("frame %d of the anti-air presses a diagonal: %#b", age, bits)
		}
	}
}

// Directions are absolute on the way out and the sim decides what forward
// means, exactly as for a keyboard: the same plan pressed by a player facing
// the other way is the mirror image.
func TestPlansPressTowardsTheOpponent(t *testing.T) {
	if got := aiPress(aiApproach, 0, 1); got != InRight {
		t.Errorf("approach facing right pressed %#b", got)
	}
	if got := aiPress(aiApproach, 0, -1); got != InLeft {
		t.Errorf("approach facing left pressed %#b", got)
	}
	if got := aiPress(aiBlock, 0, 1); got != InLeft {
		t.Errorf("block facing right pressed %#b, want away", got)
	}
}

// A button is pressed, not held: the sim reads a press as an edge, and a plan
// that kept the button down would eat the next decision's press.
func TestAButtonIsPressedOnce(t *testing.T) {
	if got := aiPress(aiPunish, 0, 1); got != InHP {
		t.Errorf("the punish pressed %#b on its first frame", got)
	}
	for age := int32(1); age < 8; age++ {
		if got := aiPress(aiPunish, age, 1); got != 0 {
			t.Errorf("the punish is still holding %#b at frame %d", got, age)
		}
	}
}
