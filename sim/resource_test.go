package sim

import "testing"

// The gauges are fields of GameState, so they roll back with everything else —
// that is covered by the layout invariant and the rollback test, not repeated
// here. What is tested here is the arithmetic: what a block costs, when Burnout
// starts and ends, and who builds meter from a hit.

// blocked runs player 0's jab into a player 1 who is holding away, and stops on
// the frame the block lands. Stopping there matters: blockstun expires, and a
// check made after the move recovered would pass for a build that produced no
// blockstun at all.
func blocked(t *testing.T, s *GameState) {
	t.Helper()
	s.Advance([2]uint16{InLP, InRight})
	for range 60 {
		s.Advance([2]uint16{0, InRight})
		if s.Players[1].State == StateBlockstun {
			return
		}
		if s.Players[1].State == StateHitstun {
			t.Fatal("holding away produced hitstun, not blockstun")
		}
	}
	t.Fatal("the attack never connected, so nothing was blocked")
}

func TestDriveStartsFullAndSuperStartsEmpty(t *testing.T) {
	s := New()
	for i := range s.Players {
		if s.Players[i].Drive != DriveMax {
			t.Errorf("p%d starts with %d Drive, want a full %d", i, s.Players[i].Drive, DriveMax)
		}
		if s.Players[i].Super != 0 {
			t.Errorf("p%d starts with %d Super, want none", i, s.Players[i].Super)
		}
	}
}

// Blocking spends Drive. This is the pressure loop: defence costs a resource,
// and the resource running out is Burnout.
func TestBlockingSpendsDrive(t *testing.T) {
	s := facing(30)
	blocked(t, &s)

	spent := DriveMax - s.Players[1].Drive
	b := BalanceOf()
	// Regeneration runs on the frames before the block lands, so the drop is
	// the cost less a few frames of it — never more than the cost, and never
	// nothing.
	if spent > b.DriveBlockCost || spent < b.DriveBlockCost-100 {
		t.Errorf("blocking moved the gauge by %d, want about the %d it costs", spent, b.DriveBlockCost)
	}
	if s.Players[0].Drive != DriveMax {
		t.Errorf("the attacker's gauge moved to %d; only the defender pays", s.Players[0].Drive)
	}
}

// Regeneration is slow in neutral, faster walking in. The asymmetry is the
// point: moving forward is rewarded.
func TestDriveRegeneratesFasterWalkingForward(t *testing.T) {
	b := BalanceOf()

	neutral, forward := New(), New()
	neutral.Players[0].Drive = 0
	forward.Players[0].Drive = 0

	for range 10 {
		neutral.Advance([2]uint16{0, 0})
		forward.Advance([2]uint16{InRight, 0})
	}

	if forward.Players[0].State != StateWalkF {
		t.Fatalf("setup: player 0 is in state %d, not walking forward", forward.Players[0].State)
	}
	if got, want := neutral.Players[0].Drive, 10*b.DriveRegen; got != want {
		t.Errorf("neutral regenerated %d over 10 frames, want %d", got, want)
	}
	if got, want := forward.Players[0].Drive, 10*b.DriveRegenWalkF; got != want {
		t.Errorf("walking forward regenerated %d over 10 frames, want %d", got, want)
	}
}

// Blockstun pauses regeneration. Without this the gauge the attacker is
// draining refills between their hits and the pressure loop never closes.
func TestBlockstunPausesDriveRegen(t *testing.T) {
	s := facing(30)
	blocked(t, &s)

	before := s.Players[1].Drive
	for range 5 {
		s.Advance([2]uint16{0, InRight})
		if s.Players[1].State != StateBlockstun {
			t.Fatal("setup: left blockstun before the check finished")
		}
	}
	if s.Players[1].Drive != before {
		t.Errorf("the gauge moved from %d to %d during blockstun", before, s.Players[1].Drive)
	}
}

func TestBurnoutStartsWhenTheGaugeEmpties(t *testing.T) {
	s := facing(30)
	s.Players[1].Drive = 1 // one unit left: the next block empties it

	blocked(t, &s)

	if s.Players[1].Burnout == 0 {
		t.Error("the gauge emptied without entering Burnout")
	}
	if s.Players[1].Drive < 0 {
		t.Errorf("Drive went negative: %d", s.Players[1].Drive)
	}
}

// Burnout ends when the gauge is *full*, not when it is above zero. Ending it
// early would hand the Drive mechanics back for one bar and make being burnt
// out a formality.
func TestBurnoutEndsOnlyWhenTheGaugeIsFull(t *testing.T) {
	s := New()
	s.Players[0].Drive, s.Players[0].Burnout = 0, 1

	frames := 0
	for range 600 {
		s.Advance([2]uint16{0, 0})
		frames++
		if s.Players[0].Burnout == 0 {
			break
		}
		if s.Players[0].Drive >= DriveMax {
			t.Fatalf("still burnt out on frame %d with a full gauge", frames)
		}
	}

	if s.Players[0].Burnout != 0 {
		t.Fatalf("still burnt out after %d frames with %d Drive", frames, s.Players[0].Drive)
	}
	if s.Players[0].Drive != DriveMax {
		t.Errorf("Burnout ended at %d Drive, want the full %d", s.Players[0].Drive, DriveMax)
	}
	if want := DriveMax / BalanceOf().DriveRegenBurnout; int32(frames) != want {
		t.Errorf("Burnout lasted %d frames, want %d at the balance's refill rate", frames, want)
	}
}

// burnt puts player 1 in Burnout for the whole test: the refill rate is set to
// zero so the recovery clock cannot run out mid-test and turn a Burnout
// assertion into a healthy-gauge one.
func burnt(t *testing.T, s *GameState) {
	t.Helper()
	b := testBalance()
	b.DriveRegenBurnout = 0
	LoadBalance(b)
	t.Cleanup(func() { LoadBalance(testBalance()) })

	s.Players[1].Drive, s.Players[1].Burnout = 0, 1
}

// The Burnout penalties: longer blockstun, and chip damage on a blocked
// special — the only situation in the game where blocking deals damage (D32).
func TestBurnoutLengthensBlockstun(t *testing.T) {
	s := facing(30)
	burnt(t, &s)
	blocked(t, &s)

	want := char().Moves[0].Blockstun + BalanceOf().BurnoutBlockstun
	if got := s.Players[1].Stun; got != want {
		t.Errorf("blockstun in Burnout = %d, want %d", got, want)
	}
}

func TestBurnoutChipsOnlyOnSpecials(t *testing.T) {
	t.Run("a blocked normal still costs nothing", func(t *testing.T) {
		s := facing(30)
		burnt(t, &s)
		full := s.Players[1].Health

		blocked(t, &s)

		if s.Players[1].Health != full {
			t.Errorf("a blocked normal chipped %d in Burnout", full-s.Players[1].Health)
		}
	})

	t.Run("a blocked special chips", func(t *testing.T) {
		s := facing(60)
		burnt(t, &s)
		full := s.Players[1].Health
		mv := &char().Moves[2] // the fireball: a special, so it chips

		feed(&s, 2, 3)
		s.Advance([2]uint16{pad[6] | InLP, InRight})
		for range 60 {
			s.Advance([2]uint16{0, InRight})
			if s.Players[1].State == StateBlockstun {
				break
			}
		}
		if s.Players[1].State != StateBlockstun {
			t.Fatal("the fireball was never blocked")
		}

		want := mv.Damage * BalanceOf().BurnoutChipPercent / 100
		if got := full - s.Players[1].Health; got != want {
			t.Errorf("chip damage = %d, want %d", got, want)
		}
	})
}

// A burnt-out gauge is already empty and refilling. Taking from it again would
// extend Burnout for as long as the pressure lasts, which is a loop with no
// exit.
func TestBurnoutTakesNoFurtherDrive(t *testing.T) {
	s := facing(30)
	s.Players[1].Drive, s.Players[1].Burnout = 0, 1

	blocked(t, &s)

	if s.Players[1].Drive <= 0 {
		t.Errorf("the gauge is at %d after blocking in Burnout: it should only be refilling", s.Players[1].Drive)
	}
}

// Both fighters build Super from the same hit, at different rates: the one
// landing it is rewarded, the one eating it is compensated.
func TestSuperBuildsForBothFightersOnAHit(t *testing.T) {
	s := facing(30)
	mv := &char().Moves[0]
	b := BalanceOf()

	for range 60 {
		s.Advance([2]uint16{InLP, 0})
		if s.Players[1].State == StateHitstun {
			break
		}
	}
	if s.Players[1].State != StateHitstun {
		t.Fatal("the jab never connected")
	}

	if got, want := s.Players[0].Super, mv.Damage*b.SuperDealtPercent/100; got != want {
		t.Errorf("the attacker built %d Super, want %d", got, want)
	}
	if got, want := s.Players[1].Super, mv.Damage*b.SuperTakenPercent/100; got != want {
		t.Errorf("the defender built %d Super, want %d", got, want)
	}
}

// A special pays a flat bonus for connecting, whether it hit or was blocked:
// it is paid for the connect, not for the damage behind it.
func TestALandedSpecialPaysItsBonus(t *testing.T) {
	for _, tc := range []struct {
		name    string
		defend  uint16
		damaged bool
	}{
		{"on hit", 0, true},
		{"on block", InRight, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := facing(60)
			mv := &char().Moves[2]
			b := BalanceOf()

			feed(&s, 2, 3)
			s.Advance([2]uint16{pad[6] | InLP, tc.defend})
			for range 60 {
				s.Advance([2]uint16{0, tc.defend})
				if s.Players[1].State == StateHitstun || s.Players[1].State == StateBlockstun {
					break
				}
			}

			want := b.SuperOnSpecial
			if tc.damaged {
				want += mv.Damage * b.SuperDealtPercent / 100
			}
			if got := s.Players[0].Super; got != want {
				t.Errorf("the fireball built %d Super, want %d", got, want)
			}
		})
	}
}

// Super does not regenerate. That asymmetry with Drive is deliberate: Drive is
// a per-round tactical resource, Super a per-match strategic one.
func TestSuperNeverRegenerates(t *testing.T) {
	s := New()
	run(&s, 300, [2]uint16{0, 0})
	if s.Players[0].Super != 0 {
		t.Errorf("Super regenerated to %d over 300 idle frames", s.Players[0].Super)
	}
}

func TestSuperIsCapped(t *testing.T) {
	var p PlayerState
	p.gainSuper(SuperMax + BarUnits)
	if p.Super != SuperMax {
		t.Errorf("Super = %d, want it capped at %d", p.Super, SuperMax)
	}
}

// A bar is a round number of units so that a half-bar cost is an exact integer.
// The design note is explicit about it, and the fractional Drive costs depend
// on it.
func TestABarIsAnEvenNumberOfUnits(t *testing.T) {
	if BarUnits%2 != 0 {
		t.Errorf("BarUnits is %d: a half-bar cost would not be an integer", BarUnits)
	}
}
