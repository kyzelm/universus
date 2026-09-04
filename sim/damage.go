package sim

// Damage scaling. Without it any combo that loops kills; with it long combos
// are possible *and* survivable, which the design note calls the single most
// balance-critical formula in the game.
//
// **The order of operations is part of the deterministic spec.** Reordering it
// changes results by a few units, and a few units is a different health value,
// a different checksum and a desync with no other symptom. All integer, all
// divisions after their multiplications, all truncating.
//
//	final = base
//	final = final * starterScale    / 100
//	final = final * comboScale[n]   / 100
//	final = max(final, minDamage)
//	final = final * counterHitBonus / 100

// ComboScaleSteps is the length of the scaling table: one entry per hit, and
// the last entry is the floor every hit past it uses.
//
// The floor is the point. Without it a long combo deals literally zero and the
// game grows degenerate loops that do nothing but burn the clock; with it every
// hit still means something and the combo terminates the round eventually.
const ComboScaleSteps = 10

// Counter hit classes, stored on the defender and shown in the HUD — a counter
// hit that is not visible is one nobody learns from.
const (
	CounterNone int32 = iota
	// CounterHit is a hit landed during the opponent's startup or active
	// frames: they were attacking and lost the exchange.
	CounterHit
	// CounterPunish is a hit landed during their recovery — the punish they
	// left open. Worth more hitstun than a counter, which is what turns a
	// whiffed heavy into a full combo.
	CounterPunish
)

// counterClass reports what kind of hit is about to land on the defender. Read
// before anything is applied: the defender's own attack is still running at
// this point, and the answer stops being available the moment they enter
// hitstun.
func (s *GameState) counterClass(defender int) int32 {
	dp := &s.Players[defender]
	mv := dp.move()
	if mv == nil {
		return CounterNone
	}
	if dp.StateFrame < mv.Startup+mv.Active {
		return CounterHit
	}
	return CounterPunish
}

// starterScale is the multiplier the move that *begins* a combo applies to the
// whole of it. Derived from the move rather than authored: a light is a light
// because of the button it is on, a special is a special because of its motion,
// and a move that had to state its own class could state it wrongly.
//
// This is what stops a jab from leading to the same damage as a heavy.
func starterScale(m *Move) int32 {
	if m.Super > 0 || m.Motion != MotionNone {
		return balance.StarterHeavy
	}
	switch m.Button {
	case InLP, InLK:
		return balance.StarterLight
	case InMP, InMK:
		return balance.StarterMedium
	default:
		return balance.StarterHeavy
	}
}

// comboScale is the multiplier for the nth hit of a combo, 1-based. Past the
// end of the table every hit takes the last entry, which is the floor.
func comboScale(n int32) int32 {
	if n < 1 {
		n = 1
	}
	if n > ComboScaleSteps {
		n = ComboScaleSteps
	}
	return balance.ComboScale[n-1]
}

// scaledDamage runs the formula above for a hit landing on the defender.
//
// hit is the 1-based index of this hit within the defender's current combo, and
// starter the multiplier captured when the combo began — passed in rather than
// read back, because the combo counter is incremented by the caller and a
// function that both reads and advances it would make the order of two
// statements a balance decision.
func scaledDamage(m *Move, starter, hit, counter int32) int32 {
	d := m.Damage
	d = d * starter / 100
	d = d * comboScale(hit) / 100

	// The floor is per-move and proportional: a scaled super doing forty damage
	// reads as a bug to the player holding the controller.
	//
	// ponytail: one global percentage rather than a minDamage field on all
	// twenty-four moves. It is the same number on every move that has been
	// authored so far; the field is one line away the day a move wants its own.
	if lo := m.Damage * balance.MinDamagePercent / 100; d < lo {
		d = lo
	}

	if counter != CounterNone {
		d = d * balance.CounterHitPercent / 100
	}
	return d
}

// counterHitstun is the extra hitstun a counter carries. A counter that only
// paid more damage would be a number on the screen; the extra frames are what
// make it open a combo that is otherwise impossible.
func counterHitstun(counter int32) int32 {
	switch counter {
	case CounterHit:
		return balance.CounterHitstun
	case CounterPunish:
		return balance.PunishHitstun
	}
	return 0
}

// chip is damage that cannot kill: health floors at 1 rather than 0.
//
// Standard, and it exists to prevent the blocked-fireball death, which is the
// most unsatisfying way to lose a round in any game that allows it. Separate
// from hurt so the clamp cannot be applied to the wrong source — a real hit
// kills, and chip is the only thing in the game that does not.
func (p *PlayerState) chip(n int32) {
	p.Health -= n
	if p.Health < 1 {
		p.Health = 1
	}
}
