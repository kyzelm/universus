package sim

import "testing"

// What the Drive gauge buys (03 Game Design/Resource System.md): since D115 cut
// the other Drive mechanics, EX specials are the only moves with a Drive price,
// so what is under test here is the price and armour.
//
// Fixture: move 13 is an armoured one-bar move on HP+HK, move 14 the two-bar EX
// special sharing move 2's motion and one of its buttons.

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

// **No EX specials at all in Burnout**, however much has refilled. The
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
