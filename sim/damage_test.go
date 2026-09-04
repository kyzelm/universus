package sim

import "testing"

// The damage pipeline is a formula, so most of it is tested as one: the order
// of operations is the part that must never move, and an integration test that
// happens to agree with a reordered formula would not notice.

func TestScalingFollowsTheTable(t *testing.T) {
	m := Move{Damage: 1000, Button: InHP} // heavy, so the starter is 100%

	// The table, hit by hit, and then the floor holding past the end of it.
	for hit, want := range map[int32]int32{
		1: 1000, 2: 1000, 3: 800, 4: 700, 5: 600,
		6: 500, 7: 400, 8: 300, 9: 200, 10: 100,
		11: 100, 40: 100,
	} {
		if got := scaledDamage(&m, 100, hit, CounterNone); got != want {
			t.Errorf("hit %d dealt %d, want %d", hit, got, want)
		}
	}
}

// The floor is not decoration. Without it a long combo deals literally zero and
// the game grows loops that do nothing but burn the clock.
func TestScalingNeverReachesZero(t *testing.T) {
	m := Move{Damage: 300, Button: InLP}
	for hit := int32(1); hit < 60; hit++ {
		if got := scaledDamage(&m, 80, hit, CounterNone); got <= 0 {
			t.Fatalf("hit %d dealt %d", hit, got)
		}
	}
}

// Minimum damage is a floor under the scaling, so a super deep in a combo does
// not arrive as forty points.
func TestMinimumDamageHoldsUnderTheScaling(t *testing.T) {
	m := Move{Damage: 3600, Button: InHP} // a level 3
	want := m.Damage * BalanceOf().MinDamagePercent / 100

	if got := scaledDamage(&m, 100, 20, CounterNone); got != want {
		t.Errorf("a fully scaled super dealt %d, want the %d floor", got, want)
	}
}

// The order of operations is part of the deterministic spec. This is the case
// that tells the specified order from the plausible one: the counter bonus is
// applied *after* the minimum-damage clamp, so a hit that has been floored
// still gets its bonus. Multiplying before the clamp gives 12 here, not 15.
func TestCounterBonusIsAppliedAfterTheFloor(t *testing.T) {
	m := Move{Damage: 100, Button: InLP}

	// 100 → starter 80 → hit 10 leaves 8 → floored to 10 → counter 150%.
	if got, want := scaledDamage(&m, 80, 10, CounterHit), int32(15); got != want {
		t.Errorf("floored counter hit dealt %d, want %d — the bonus must come last", got, want)
	}
}

// The starter is derived from the move, never authored: a light is a light
// because of its button, a special because of its motion.
func TestStarterScaleIsDerived(t *testing.T) {
	b := BalanceOf()
	for _, tc := range []struct {
		name string
		m    Move
		want int32
	}{
		{"light punch", Move{Button: InLP}, b.StarterLight},
		{"light kick", Move{Button: InLK}, b.StarterLight},
		{"medium punch", Move{Button: InMP}, b.StarterMedium},
		{"medium kick", Move{Button: InMK}, b.StarterMedium},
		{"heavy punch", Move{Button: InHP}, b.StarterHeavy},
		{"heavy kick", Move{Button: InHK}, b.StarterHeavy},
		// A special is a heavy starter whatever button carries it, and a super
		// is one whatever else it is.
		{"a light-button special", Move{Button: InLP, Motion: MotionQCF}, b.StarterHeavy},
		{"a light-button super", Move{Button: InLP, Motion: MotionQCFx2, Super: 1}, b.StarterHeavy},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := starterScale(&tc.m); got != tc.want {
				t.Errorf("starter = %d, want %d", got, tc.want)
			}
		})
	}
}

// A counter hit is a hit landed while the opponent's own attack is in its
// startup or active frames; a punish counter is one landed in the recovery.
// Both are read off the defender's state before anything is applied to it.
func TestCounterClassReadsTheDefendersMove(t *testing.T) {
	mv := &char().Moves[0] // 4 startup, 3 active, 6 recovery

	for _, tc := range []struct {
		frame int32
		want  int32
	}{
		{0, CounterHit},
		{mv.Startup - 1, CounterHit},
		{mv.Startup, CounterHit},
		{mv.Startup + mv.Active - 1, CounterHit},
		{mv.Startup + mv.Active, CounterPunish},
		{mv.Total() - 1, CounterPunish},
	} {
		s := New()
		p := &s.Players[1]
		p.State, p.MoveIndex, p.StateFrame = StateAttack, 0, tc.frame

		if got := s.counterClass(1); got != tc.want {
			t.Errorf("frame %d of the move classed as %d, want %d", tc.frame, got, tc.want)
		}
	}

	// Anyone not attacking is not counter-hittable.
	idle := New()
	if got := idle.counterClass(1); got != CounterNone {
		t.Errorf("an idle defender classed as %d", got)
	}
}

// A counter hit has to pay in frames as well as damage: the extra hitstun is
// what opens a combo that is otherwise impossible, and the damage on its own
// would just be a bigger number.
func TestACounterHitPaysDamageAndFrames(t *testing.T) {
	clean := facing(30)
	clean.Advance([2]uint16{InLP, 0})
	for clean.Players[1].State != StateHitstun {
		clean.Advance([2]uint16{0, 0})
	}

	traded := facing(30)
	traded.Advance([2]uint16{InLP, InLP}) // both attacking, so both counter
	for traded.Players[1].State != StateHitstun {
		traded.Advance([2]uint16{0, 0})
	}

	if clean.Players[1].Counter != CounterNone {
		t.Errorf("a hit on an idle defender was flagged %d", clean.Players[1].Counter)
	}
	if traded.Players[1].Counter != CounterHit {
		t.Fatalf("the trade was flagged %d, want a counter hit", traded.Players[1].Counter)
	}

	if traded.Players[1].Stun <= clean.Players[1].Stun {
		t.Errorf("counter hitstun %d is not above the normal %d",
			traded.Players[1].Stun, clean.Players[1].Stun)
	}
	if traded.Players[1].Health >= clean.Players[1].Health {
		t.Errorf("the counter hit cost %d, the clean hit %d",
			10000-traded.Players[1].Health, 10000-clean.Players[1].Health)
	}
}

// A combo lasts as long as the hitstun holding it together. The moment the
// defender recovers, the next hit is a first hit again — at full damage and
// with a fresh starter.
func TestTheComboEndsWithTheHitstun(t *testing.T) {
	s := facing(30)
	full := s.Players[1].Health

	s.Advance([2]uint16{InLP, 0})
	for s.Players[1].State != StateHitstun {
		s.Advance([2]uint16{0, 0})
	}
	if got := s.Players[1].Combo; got != 1 {
		t.Fatalf("combo counter = %d after one hit", got)
	}
	if got := s.Players[1].ComboStarter; got != BalanceOf().StarterLight {
		t.Errorf("starter = %d, want the jab's light %d", got, BalanceOf().StarterLight)
	}
	first := full - s.Players[1].Health

	// Let the hitstun run out, then hit again.
	for s.Players[1].State == StateHitstun || s.Hitstop > 0 {
		s.Advance([2]uint16{0, 0})
	}
	if got := s.Players[1].Combo; got != 0 {
		t.Fatalf("combo counter = %d after recovery, want it reset", got)
	}

	before := s.Players[1].Health
	s.Advance([2]uint16{InLP, 0})
	for s.Players[1].State != StateHitstun {
		s.Advance([2]uint16{0, 0})
	}
	if got := before - s.Players[1].Health; got != first {
		t.Errorf("the second combo's opening hit dealt %d, want the same %d as the first", got, first)
	}
}

// The second hit of a combo is the one that proves the counter is doing
// anything: it scales, and it scales by the starter the *first* move set.
func TestTheSecondHitOfACombo(t *testing.T) {
	s := facing(30)
	dp := &s.Players[1]

	// Land the jab, then cancel it into the level 1 super while the defender is
	// still in hitstun — the fixture's jab names super1 as a cancel target
	// exactly so a two-hit combo can be built without a second character.
	s.Players[0].Super = 3 * BarUnits
	connect(t, &s, InLP)
	if dp.Combo != 1 {
		t.Fatalf("setup: combo counter = %d after the jab", dp.Combo)
	}

	before := dp.Health
	qcfx2(&s, InLP)
	if s.Players[0].MoveIndex != 8 {
		t.Fatalf("setup: move %d came out of the cancel, want the level 1", s.Players[0].MoveIndex)
	}
	for range 30 {
		s.Advance([2]uint16{0, 0})
		if dp.Combo == 2 {
			break
		}
	}
	if dp.Combo != 2 {
		t.Fatal("the super never connected, so there was no second hit")
	}

	// 400 base, the jab's light starter, second hit at 100%.
	const want = 400 * 80 / 100
	if got := before - dp.Health; got != want {
		t.Errorf("the second hit dealt %d, want %d — the starter is the jab's, not the super's", got, want)
	}
}

// Chip cannot kill. Losing to a blocked fireball is the least satisfying way to
// lose a round in any game that allows it.
func TestChipCannotKill(t *testing.T) {
	s := facing(60)
	burnt(t, &s)
	s.Players[1].Health = 1

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

	if got := s.Players[1].Health; got != 1 {
		t.Errorf("health = %d after chip on one point of health", got)
	}
}
