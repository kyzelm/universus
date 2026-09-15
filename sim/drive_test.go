package sim

import "testing"

// The Drive mechanics (03 Game Design/Resource System.md). Three of the five —
// Impact, EX, Reversal — are moves with a price, so what is under test here is
// the price and the one mechanism they needed that did not exist: armour.
//
// Fixture: move 13 is the armoured one-bar Impact on HP+HK, move 14 the
// two-bar EX special sharing move 2's motion and one of its buttons.

const (
	moveArmored = 13
	moveEX      = 14
)

// qcf inputs a quarter-circle and presses the buttons on the final frame, the
// way every other motion test in this package does.
func qcf(s *GameState, button uint16) {
	feed(s, 2, 3)
	s.Advance([2]uint16{pad[6] | button, 0})
}

// The price is checked at selection, exactly like a super's: the EX wins the
// press it shares with the cheaper special, and the bars are gone afterwards.
func TestEXCostsDriveAndOutranksTheSpecialItSharesAButtonWith(t *testing.T) {
	s := New()
	before := s.Players[0].Drive
	qcf(&s, InLP|InMP)

	if got := s.Players[0].MoveIndex; got != moveEX {
		t.Fatalf("move = %d, want the EX special (%d)", got, moveEX)
	}
	// Two bars, less the frame of regeneration that runs after the move comes
	// out — the gauge refills on the same frame it was charged, so an equality
	// against two bars would be testing the regeneration rate by accident.
	spent := before - s.Players[0].Drive
	if want := 2*BarUnits - BalanceOf().DriveRegen; spent != want {
		t.Errorf("spent %d units, want %d (two bars, one frame of regen back)", spent, want)
	}
}

// The gauge it cannot pay for leaves the cheaper move to win the press — the
// same courtesy the meter gives the fireball when a super is unaffordable. A
// move that was selected and then failed would have already beaten it.
func TestAnUnaffordableEXComesOutAsThePlainSpecial(t *testing.T) {
	s := New()
	s.Players[0].Drive = BarUnits // one bar against the EX's two
	qcf(&s, InLP|InMP)

	if got := s.Players[0].MoveIndex; got != 2 {
		t.Fatalf("move = %d, want the plain special (2)", got)
	}
	if s.Players[0].Drive < BarUnits {
		t.Errorf("drive fell to %d: an unaffordable move was charged for", s.Players[0].Drive)
	}
}

// **No Drive mechanics at all in Burnout**, however much has refilled. The
// gauge climbing back is not the same as being allowed to spend it, and this is
// what makes running out a state worth avoiding.
func TestBurnoutRefusesDriveMovesEvenWithBarsBack(t *testing.T) {
	s := New()
	s.Players[0].Burnout = 1
	// Half a gauge: more than the EX costs, and far enough from full that the
	// Burnout refill cannot end the state before the motion is finished.
	s.Players[0].Drive = DriveMax / 2
	qcf(&s, InLP|InMP)

	if got := s.Players[0].MoveIndex; got != 2 {
		t.Errorf("move = %d, want the plain special (2) — Burnout allows no EX", got)
	}
}

// armored starts the armoured move on player 1 and returns the state on the
// frame after it came out, with the two players close enough for move 0 to
// reach. Player 1 is the defender in these tests: the armour belongs to the
// player being hit.
func armored(t *testing.T, gap int) GameState {
	t.Helper()
	s := facing(gap)
	s.Advance([2]uint16{0, InHP | InHK})

	if got := s.Players[1].MoveIndex; got != moveArmored {
		t.Fatalf("move = %d, want the armoured move (%d)", got, moveArmored)
	}
	return s
}

// The mechanism the bar buys: the hit lands, the move keeps going.
func TestArmorAbsorbsAHitWithoutStoppingTheMove(t *testing.T) {
	s := armored(t, 40)
	health := s.Players[1].Health

	// Player 0 jabs into it. The jab connects during the armoured move's
	// startup, which is the window armour exists for.
	for f := 0; f < 8 && s.Players[1].State == StateAttack; f++ {
		s.Advance([2]uint16{InLP, 0})
	}

	if s.Players[1].State != StateAttack {
		t.Fatalf("the armoured player is in state %d — the hit stopped the move", s.Players[1].State)
	}
	if s.Players[1].Stun != 0 {
		t.Errorf("stun = %d, want none: an absorbed hit costs no hitstun", s.Players[1].Stun)
	}
	if s.Players[1].Armor != 1 {
		t.Fatalf("armor = %d, want 1 hit absorbed", s.Players[1].Armor)
	}

	// Damage is reduced, not nothing: armouring through a heavy has a price.
	// The fixture's jab deals 100 and the fixture absorbs at 50%.
	if lost := health - s.Players[1].Health; lost != 50 {
		t.Errorf("lost %d health, want the fixture's halved jab (50)", lost)
	}
}

// One hit of armour is one hit. The second lands like any other, or the move
// is not armoured but invincible.
func TestTheSecondHitIsNotAbsorbed(t *testing.T) {
	s := armored(t, 40)
	s.Players[1].Armor = 1 // the first hit is already spent

	for f := 0; f < 8 && s.Players[1].State == StateAttack; f++ {
		s.Advance([2]uint16{InLP, 0})
	}

	if s.Players[1].State != StateHitstun {
		t.Errorf("state = %d, want hitstun: armour was already spent", s.Players[1].State)
	}
}

// Armour cannot kill. A mechanic that trades a bar for the round is one nobody
// spends a bar on, and the chip rule the design already has says so.
func TestAnAbsorbedHitCannotKill(t *testing.T) {
	s := armored(t, 40)
	s.Players[1].Health = 10

	for f := 0; f < 8 && s.Players[1].State == StateAttack; f++ {
		s.Advance([2]uint16{InLP, 0})
	}

	if s.Players[1].Health != 1 {
		t.Errorf("health = %d, want the floor of 1", s.Players[1].Health)
	}
}

// The answer to armour. A throw is resolved before anything armour touches and
// an attacking player is throwable, so the armoured move loses to the grab —
// which is what keeps it from being a button with no response.
func TestAThrowBeatsArmor(t *testing.T) {
	s := armored(t, 40)

	for f := 0; f < 10 && s.Players[1].State == StateAttack; f++ {
		s.Advance([2]uint16{InLP | InLK, 0})
	}

	if s.Players[1].State != StateThrown {
		t.Errorf("state = %d, want thrown: a throw goes through armour", s.Players[1].State)
	}
	if s.Players[1].Armor != 0 {
		t.Errorf("armor = %d: the throw was absorbed instead of landing", s.Players[1].Armor)
	}
}

// Armour covers the frames that carry the move, not the recovery it is exposed
// on afterwards. An armoured move that were safe as well as strong is the one
// nobody would ever not press.
func TestArmorDoesNotCoverTheRecovery(t *testing.T) {
	// Out of range to begin with: the armoured move's own hitbox would
	// otherwise knock the attacker down during its active frames, and the
	// recovery would be tested against somebody lying on the floor.
	s := armored(t, 200)
	mv := &char().Moves[moveArmored]

	// Sit through the active window without pressing anything, so the whiffed
	// move reaches its recovery with its armour unspent.
	for s.Players[1].StateFrame < mv.Startup+mv.Active {
		s.Advance([2]uint16{0, 0})
	}

	// Now close the distance, so what lands during the recovery is a jab and
	// not the exchange that never happened.
	s.Players[0].X, s.Players[1].X = FromInt(-20), FromInt(20)
	if s.Players[1].Armor != 0 {
		t.Fatalf("armor = %d: something hit it during the active frames", s.Players[1].Armor)
	}

	for f := 0; f < 8 && s.Players[1].State == StateAttack; f++ {
		s.Advance([2]uint16{InLP, 0})
	}

	if s.Players[1].State != StateHitstun {
		t.Errorf("state = %d, want hitstun: the recovery is not armoured", s.Players[1].State)
	}
}

// ---- Drive Reversal --------------------------------------------------------

const moveReversal = 15

// blocking puts player 1 in blockstun from player 0's jab, holding back, and
// returns on the first frame of it. Two bars are left in the gauge whatever
// blocking cost, so what the tests below measure is the reversal's own price.
func blockstun(t *testing.T) GameState {
	t.Helper()
	s := facing(40)

	for f := 0; f < 12 && s.Players[1].State != StateBlockstun; f++ {
		s.Advance([2]uint16{InLP, InRight}) // player 1 faces left, so right is back
	}
	if s.Players[1].State != StateBlockstun {
		t.Fatalf("player 1 is in state %d, want blockstun", s.Players[1].State)
	}

	// Past the hitstop first. Both fighters are frozen for it and no input is
	// resolved, so a reversal pressed on the frame of the block is a press the
	// buffer has to carry — which is its own behaviour and tested elsewhere.
	for s.Hitstop > 0 {
		s.Advance([2]uint16{0, InRight})
	}
	if s.Players[1].State != StateBlockstun {
		t.Fatalf("the blockstun ended with the hitstop, in state %d", s.Players[1].State)
	}

	s.Players[1].Drive = DriveMax
	return s
}

// The mechanic: two bars and the pressure is over.
func TestDriveReversalComesOutOfBlockstun(t *testing.T) {
	s := blockstun(t)
	before := s.Players[1].Drive

	s.Advance([2]uint16{0, InHP | InHK})

	if got := s.Players[1].MoveIndex; got != moveReversal {
		t.Fatalf("move = %d, want the reversal (%d)", got, moveReversal)
	}
	if s.Players[1].State != StateAttack {
		t.Errorf("state = %d, want the attack: the reversal left blockstun", s.Players[1].State)
	}
	if s.Players[1].Stun != 0 {
		t.Errorf("stun = %d, want none: the blockstun it escaped is over", s.Players[1].Stun)
	}
	if spent := before - s.Players[1].Drive; spent != 2*BarUnits-BalanceOf().DriveRegen {
		t.Errorf("spent %d units, want two bars less a frame of regen", spent)
	}
}

// Blockstun is still blockstun. The reversal is the exception, and an exception
// that let every other button through would be blockstun with extra steps.
func TestNothingElseComesOutOfBlockstun(t *testing.T) {
	s := blockstun(t)

	s.Advance([2]uint16{0, InLP})

	if s.Players[1].State != StateBlockstun {
		t.Errorf("state = %d: a jab came out of blockstun", s.Players[1].State)
	}
}

// And the price is real: without the bars, the player keeps blocking.
func TestAnUnaffordableReversalLeavesThePlayerBlocking(t *testing.T) {
	s := blockstun(t)
	s.Players[1].Drive = BarUnits

	s.Advance([2]uint16{0, InHP | InHK})

	if s.Players[1].State != StateBlockstun {
		t.Errorf("state = %d, want blockstun: one bar cannot pay for a reversal", s.Players[1].State)
	}
}

// The reversal is not a move to press in neutral. It exists to answer pressure,
// and a version available standing is an invincible two-bar button.
func TestTheReversalCannotBePressedInNeutral(t *testing.T) {
	s := New()

	s.Advance([2]uint16{InHP | InHK, 0})

	if got := s.Players[0].MoveIndex; got == moveReversal {
		t.Error("the reversal came out of an actionable state")
	}
}

// ---- Drive Parry -----------------------------------------------------------

// The stance: held, and it costs while it is held. A charge on the press would
// make a parry held all round as cheap as one held for three frames.
func TestParryIsAStanceThatDrains(t *testing.T) {
	s := New()
	before := s.Players[0].Drive

	for f := 0; f < 5; f++ {
		s.Advance([2]uint16{ParryButtons, 0})
	}

	if s.Players[0].State != StateParry {
		t.Fatalf("state = %d, want the parry", s.Players[0].State)
	}
	// Five frames at a tenth of a bar each: the design's half bar, held.
	if spent := before - s.Players[0].Drive; spent != 5*BalanceOf().DriveParryDrain {
		t.Errorf("spent %d units over five frames, want %d", spent, 5*BalanceOf().DriveParryDrain)
	}
}

// Letting go is what ends it, and nothing else comes out of it in the meantime.
func TestReleasingEndsTheParry(t *testing.T) {
	s := New()
	s.Advance([2]uint16{ParryButtons, 0})
	s.Advance([2]uint16{0, 0})

	if s.Players[0].State != StateIdle {
		t.Errorf("state = %d, want idle once the buttons are released", s.Players[0].State)
	}
}

// MP+MK is the parry and not the medium punch. The press would otherwise be
// read as a move before the stance was ever considered.
func TestTheParryInputIsNotAMediumPunch(t *testing.T) {
	s := New()
	s.Advance([2]uint16{ParryButtons, 0})

	if s.Players[0].State == StateAttack {
		t.Errorf("move %d came out instead of the parry", s.Players[0].MoveIndex)
	}
}

// The mechanic itself: absorbed, no damage, **no blockstun**, and the gauge
// pays for reading it right.
func TestParryAbsorbsWithoutBlockstunAndPaysBack(t *testing.T) {
	s := facing(40)
	s.Players[1].Drive = DriveMax / 2
	drive := s.Players[1].Drive
	health := s.Players[1].Health

	for f := 0; f < 8 && s.Players[1].Events&EventBlock == 0; f++ {
		s.Advance([2]uint16{InLP, ParryButtons})
	}

	if s.Players[1].State != StateParry {
		t.Fatalf("state = %d, want the parry still held", s.Players[1].State)
	}
	if s.Players[1].Stun != 0 {
		t.Errorf("stun = %d, want none: a parry takes no blockstun", s.Players[1].Stun)
	}
	if s.Players[1].Health != health {
		t.Errorf("lost %d health to a parried hit", health-s.Players[1].Health)
	}
	// The gain, less the frames of drain the hold cost.
	if s.Players[1].Drive <= drive {
		t.Errorf("drive %d did not rise from %d: the parry paid nothing back", s.Players[1].Drive, drive)
	}
}

// A throw goes through it, which is what keeps the stance from being an answer
// to everything. Same answer blocking has, for the same reason.
func TestAThrowBeatsTheParry(t *testing.T) {
	s := facing(40)

	for f := 0; f < 10 && s.Players[1].State != StateThrown; f++ {
		s.Advance([2]uint16{InLP | InLK, ParryButtons})
	}

	if s.Players[1].State != StateThrown {
		t.Errorf("state = %d, want thrown: a throw beats a parry", s.Players[1].State)
	}
}

// Burnout allows no Drive mechanics, and the parry is one of them.
func TestBurnoutRefusesTheParry(t *testing.T) {
	s := New()
	s.Players[0].Burnout = 1
	s.Players[0].Drive = DriveMax / 2

	s.Advance([2]uint16{ParryButtons, 0})

	if s.Players[0].State == StateParry {
		t.Error("a burnt-out player parried")
	}
}

// Holding it to empty is Burnout like any other way of reaching zero — and the
// stance ends on the frame the gauge does.
func TestParryingToEmptyBurnsOutAndEndsTheStance(t *testing.T) {
	s := New()
	s.Players[0].Drive = BalanceOf().DriveParryDrain

	s.Advance([2]uint16{ParryButtons, 0}) // the last frame it can pay for
	if s.Players[0].Burnout == 0 {
		t.Fatalf("drive = %d and no burnout: the drain did not empty the gauge", s.Players[0].Drive)
	}

	s.Advance([2]uint16{ParryButtons, 0})
	if s.Players[0].State == StateParry {
		t.Error("the parry continued into Burnout")
	}
}

// ---- Drive Rush ------------------------------------------------------------

// tapForward taps forward twice with a gap, which is what the dash reader
// wants: two presses, not one hold. Buttons may be held across the whole of it,
// which is how a rush out of the parry is actually input.
func tapForward(s *GameState, hold uint16) {
	s.Advance([2]uint16{hold | InRight, 0})
	s.Advance([2]uint16{hold, 0})
	s.Advance([2]uint16{hold | InRight, 0})
}

// One bar out of the stance. The cheap entry, and the reason holding a parry is
// not purely defensive.
func TestRushOutOfTheParryCostsOneBar(t *testing.T) {
	s := New()
	s.Advance([2]uint16{ParryButtons, 0})
	before := s.Players[0].Drive

	tapForward(&s, ParryButtons)

	if s.Players[0].State != StateRush {
		t.Fatalf("state = %d, want the rush", s.Players[0].State)
	}
	// The parry's drain runs for the frames the taps took, so the comparison is
	// "at least the bar", not "exactly".
	if spent := before - s.Players[0].Drive; spent < BalanceOf().DriveRushCost {
		t.Errorf("spent %d units, want at least the rush's %d", spent, BalanceOf().DriveRushCost)
	}
}

// It is a rush and not a walk: the speed is the mechanic, and a walk would
// quietly pass a test that only checked the player moved forward.
func TestTheRushTravelsAtItsOwnSpeed(t *testing.T) {
	s := New()
	s.Advance([2]uint16{ParryButtons, 0})
	tapForward(&s, ParryButtons)

	x := s.Players[0].X
	s.Advance([2]uint16{0, 0})

	if moved := s.Players[0].X - x; moved != BalanceOf().DriveRushSpeed {
		t.Errorf("moved %d in a frame, want the rush speed %d", moved, BalanceOf().DriveRushSpeed)
	}
}

// What the bar buys: an attack comes out of the rush. An ordinary dash commits
// and this does not, which is the whole difference between them.
func TestAMoveComesOutOfTheRush(t *testing.T) {
	s := New()
	s.Advance([2]uint16{ParryButtons, 0})
	tapForward(&s, ParryButtons)

	s.Advance([2]uint16{InLP, 0})

	if s.Players[0].State != StateAttack {
		t.Errorf("state = %d, want the attack cancelled out of the rush", s.Players[0].State)
	}
}

// And it ends on its own, rather than running until something interrupts it.
func TestTheRushEnds(t *testing.T) {
	s := New()
	s.Advance([2]uint16{ParryButtons, 0})
	tapForward(&s, ParryButtons)

	for f := int32(0); f <= BalanceOf().DriveRushFrames; f++ {
		s.Advance([2]uint16{0, 0})
	}

	if s.Players[0].State == StateRush {
		t.Errorf("the rush is still running after %d frames", BalanceOf().DriveRushFrames)
	}
}

// Three bars out of a connected normal that names the cancel — the expensive
// entry, because what it buys is a combo rather than an approach.
func TestRushCancelCostsThreeBars(t *testing.T) {
	s := facing(40)

	// The jab connects; its cancel window opens on the hit.
	for f := 0; f < 8 && s.Players[1].State != StateBlockstun && s.Players[1].Stun == 0; f++ {
		s.Advance([2]uint16{InLP, 0})
	}
	for s.Hitstop > 0 {
		s.Advance([2]uint16{0, 0})
	}
	if s.Players[0].State != StateAttack {
		t.Fatalf("player 0 is in state %d, want the jab still running", s.Players[0].State)
	}

	before := s.Players[0].Drive
	tapForward(&s, 0)

	if s.Players[0].State != StateRush {
		t.Fatalf("state = %d, want the rush out of the cancel window", s.Players[0].State)
	}
	// The cancel's three bars, less the frame of regeneration that runs after
	// the rush starts — the same arithmetic as every other price here.
	spent := before - s.Players[0].Drive
	if want := BalanceOf().DriveRushCancelCost - BalanceOf().DriveRegen; spent != want {
		t.Errorf("spent %d units, want %d (three bars, one frame of regen back)", spent, want)
	}
}

// Burnout allows no Drive mechanics, and the rush is the fifth of them.
func TestBurnoutRefusesTheRush(t *testing.T) {
	s := New()
	s.Players[0].Burnout = 1
	s.Players[0].Drive = DriveMax / 2

	tapForward(&s, 0)

	if s.Players[0].State == StateRush {
		t.Error("a burnt-out player rushed")
	}
}
