package sim

import "testing"

// The supers are three things at once: a motion, a price and a cancel target.
// Each is tested on its own, because each fails differently — a motion that is
// not recognised is a move nobody can do, a price that is not checked is a
// level 3 on every press, and a cancel tier that is not enforced is the combo
// system with its tiering removed.

// qcfx2 and qcbx2 input the double motion and press button on the final frame,
// the way every other motion test in this package does: the last direction and
// the button land together.
func qcfx2(s *GameState, button uint16) {
	feed(s, 2, 3, 6, 2, 3)
	s.Advance([2]uint16{pad[6] | button, 0})
}

func qcbx2(s *GameState, button uint16) {
	feed(s, 2, 1, 4, 2, 1)
	s.Advance([2]uint16{pad[4] | button, 0})
}

func TestSuperMotionsAreRecognised(t *testing.T) {
	for _, tc := range []struct {
		name string
		dirs []uint8
		want Motion
	}{
		{"double quarter-circle forward", []uint8{2, 3, 6, 2, 3, 6}, MotionQCFx2},
		{"double quarter-circle back", []uint8{2, 1, 4, 2, 1, 4}, MotionQCBx2},
		// One quarter-circle is not two. The meter would otherwise be the only
		// thing between a fireball input and a level 3.
		{"a single quarter-circle is not a super motion", []uint8{2, 3, 6}, MotionQCF},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := New()
			feed(&s, tc.dirs...)
			if got := s.Motion(0); got != tc.want {
				t.Errorf("motion = %d, want %d", got, tc.want)
			}
		})
	}
}

// The doubles must be scanned before DP, and this is the reason: read backward,
// ↓↘→↓↘→ offers →, ↓, ↘ in exactly the order a dragon punch wants. A super
// motion checked after DP is a dragon punch every time.
func TestSuperMotionBeatsDP(t *testing.T) {
	s := New()
	feed(&s, 2, 3, 6, 2, 3, 6)
	if got := s.Motion(0); got != MotionQCFx2 {
		t.Fatalf("motion = %d, want the double quarter-circle (%d)", got, MotionQCFx2)
	}

	// And the plain dragon punch still is one.
	d := New()
	feed(&d, 6, 2, 3)
	if got := d.Motion(0); got != MotionDP {
		t.Errorf("a dragon punch reads as %d", got)
	}
}

// A double quarter-circle is a quarter-circle that kept going, so the same
// input has to be able to produce the cheaper move. Never the other way round.
func TestDoubleMotionSatisfiesTheSingle(t *testing.T) {
	for _, tc := range []struct {
		got, want Motion
		ok        bool
	}{
		{MotionQCFx2, MotionQCF, true},
		{MotionQCBx2, MotionQCB, true},
		{MotionQCFx2, MotionQCFx2, true},
		{MotionQCF, MotionQCFx2, false},
		{MotionQCFx2, MotionQCB, false},
		{MotionDP, MotionQCF, false},
	} {
		if got := tc.got.satisfies(tc.want); got != tc.ok {
			t.Errorf("Motion(%d).satisfies(%d) = %v, want %v", tc.got, tc.want, got, tc.ok)
		}
	}
}

// The category a move *is* — derived from its own data, never authored, so it
// cannot disagree with what the move actually does.
func TestCancelCategoryIsDerived(t *testing.T) {
	c := char()
	for _, tc := range []struct {
		move int32
		want uint16
	}{
		{0, CancelChain},   // a jab
		{2, CancelSpecial}, // a motion makes it a special
		{8, CancelSuper1},
		{9, CancelSuper3},
	} {
		if got := c.Moves[tc.move].Category(); got != tc.want {
			t.Errorf("move %d category = %#b, want %#b", tc.move, got, tc.want)
		}
	}
}

// The meter is checked when the move is *selected*, not after it comes out.
// That is what lets one input produce the fireball when the bar is not there:
// a super refused during the search leaves the cheaper move to win the press.
func TestASuperWithoutTheMeterIsTheSpecial(t *testing.T) {
	for _, tc := range []struct {
		name string
		bars int32
		want int32
	}{
		{"no meter at all", 0, 2},
		{"a fraction short", BarUnits - 1, 2},
		{"exactly the cost", BarUnits, 8},
		{"more than enough", 3 * BarUnits, 8},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := New()
			s.Players[0].Super = tc.bars
			qcfx2(&s, InLP)

			if got := s.Players[0].MoveIndex; got != tc.want {
				t.Errorf("move %d came out, want %d", got, tc.want)
			}
		})
	}
}

func TestTheLevelThreeCostsThreeBars(t *testing.T) {
	short := New()
	short.Players[0].Super = 3*BarUnits - 1
	qcbx2(&short, InMP)
	if got := short.Players[0].MoveIndex; got == 9 {
		t.Error("the level 3 came out a unit short of three bars")
	}

	paid := New()
	paid.Players[0].Super = 3 * BarUnits
	qcbx2(&paid, InMP)
	if got := paid.Players[0].MoveIndex; got != 9 {
		t.Errorf("move %d came out with three bars, want the level 3", got)
	}
}

func TestPerformingASuperSpendsItsBars(t *testing.T) {
	s := New()
	s.Players[0].Super = 3 * BarUnits
	qcfx2(&s, InLP)

	if s.Players[0].MoveIndex != 8 {
		t.Fatalf("setup: move %d came out, not the level 1", s.Players[0].MoveIndex)
	}
	if got, want := s.Players[0].Super, int32(2*BarUnits); got != want {
		t.Errorf("meter = %d after a one-bar super, want %d", got, want)
	}
	// And it is spent once, not once per frame the move is on screen.
	run(&s, 10, [2]uint16{0, 0})
	if got, want := s.Players[0].Super, int32(2*BarUnits); got != want {
		t.Errorf("meter = %d ten frames later, want %d", got, want)
	}
}

// connect runs player 0's move until it has connected and the hitstop it caused
// has run out, which is when the cancel window is open *and* inputs are being
// read again. Hitstop freezes the state machine, so a motion input entirely
// inside it never reaches resolveInputs.
func connect(t *testing.T, s *GameState, in uint16) {
	t.Helper()
	s.Advance([2]uint16{in, 0})
	for range 30 {
		if s.Players[0].HasHit != 0 && s.Hitstop == 0 {
			return
		}
		s.Advance([2]uint16{0, 0})
	}
	t.Fatal("the move never connected, so no cancel window opened")
}

// The tiering is data: a source move names the levels it feeds. A cancelable
// normal feeds level 1; the fixture's low feeds level 3 and nothing else, which
// is the heavy's tier.
func TestCancelTiersComeFromTheSource(t *testing.T) {
	t.Run("a cancelable normal reaches the level 1", func(t *testing.T) {
		s := facing(30)
		s.Players[0].Super = 3 * BarUnits
		connect(t, &s, InLP)
		qcfx2(&s, InLP)

		if got := s.Players[0].MoveIndex; got != 8 {
			t.Errorf("move %d came out of the cancel, want the level 1", got)
		}
	})

	t.Run("a source that names only the level 3 refuses the level 1", func(t *testing.T) {
		s := facing(30)
		s.Players[0].Super = 3 * BarUnits
		connect(t, &s, InDown|InLK)
		qcfx2(&s, InLP)

		// Not the level 1, and not the fireball or the jab either: the mask
		// names one category and the search takes nothing outside it.
		if got := s.Players[0].MoveIndex; got != 1 {
			t.Errorf("move %d came out, want the low still running (1)", got)
		}
	})

	t.Run("and accepts the level 3", func(t *testing.T) {
		s := facing(30)
		s.Players[0].Super = 3 * BarUnits
		connect(t, &s, InDown|InLK)
		qcbx2(&s, InMP)

		if got := s.Players[0].MoveIndex; got != 9 {
			t.Errorf("move %d came out of the cancel, want the level 3", got)
		}
	})
}
