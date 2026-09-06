package sim

// Knockback: what a hit does to the defender's *position*, and the juggle
// system that grows out of it (03 Game Design/Movement and Defense.md,
// 03 Game Design/Damage and Combo Scaling.md).
//
// Before this, nothing a hit did moved the defender at all: pushboxes separated
// and the throw tech pushed, and that was the whole of it. A blockstring
// therefore never spaced itself out, the corner did nothing on block, and no
// move could put the defender in the air — which is why the juggle counter had
// nothing to count and had been waiting on this file rather than on knockdowns.
//
// One field family, two behaviours. A move carries a knockback velocity applied
// to the defender: a vertical component launches them and gravity supplies the
// arc, and a purely horizontal one is ordinary pushback. Blocking has a number
// of its own, and it is the larger one, because it is what decides whether
// pressure continues.
//
// The corner rule is the half that matters most and it lives in clampToStage:
// when the wall stops the defender, the leftover push moves the *attacker* back
// instead. Without it the clamp eats the pushback exactly where the design says
// pressure is decided.

// Knockback is the velocity the move gives the defender, forward-relative and
// unsigned — the caller turns it away from the attacker. Blocked knockback is
// horizontal: a blocked hit does not launch anyone.
//
// Zero in the data means the balance file's default, which is what every move
// that is not a launcher wants. The alternative is authoring the same pair of
// numbers on all two dozen moves, which is two dozen chances to author it
// differently by mistake.
func (m *Move) Knockback(blocked bool) (Fix, Fix) {
	if blocked {
		return balance.KnockbackBlock, 0
	}
	if m.KnockbackVX == 0 && m.KnockbackVY == 0 {
		return balance.KnockbackHit, 0
	}
	return m.KnockbackVX, m.KnockbackVY
}

// Launcher reports a move that puts the defender in the air. Data, not a flag:
// a positive vertical knockback *is* what a launcher is, so the two cannot
// disagree.
func (m *Move) Launcher() bool { _, vy := m.Knockback(false); return vy > 0 }

// JuggleLimitOf is the juggle count at which the move stops connecting, the
// balance default standing in for the moves that do not name one.
func (m *Move) JuggleLimitOf() int32 {
	if m.JuggleLimit != 0 {
		return m.JuggleLimit
	}
	return balance.JuggleLimit
}

// applyKnockback pushes the defender away from the attacker.
//
// Away is decided by the two positions rather than by the attacker's facing:
// at the instant of a crossup the two disagree, and pushing by facing would
// pull the defender *through* the attacker on exactly the hit where the corner
// matters. The tie at identical positions breaks by facing, which is the only
// thing left that both machines agree on.
//
// ponytail: the attacker is not pushed back except by the corner rule. A game
// where blocking pushes both fighters apart is a second authored number and a
// second set of corner cases; the one that changes how pressure plays is the
// defender's, and the corner already reverses it.
func (s *GameState) applyKnockback(attacker, defender int, mv *Move, blocked bool) {
	ap, dp := &s.Players[attacker], &s.Players[defender]

	vx, vy := mv.Knockback(blocked)
	if dp.X < ap.X || (dp.X == ap.X && ap.Facing < 0) {
		vx = -vx
	}
	dp.VX, dp.VY = vx, vy
}

// pushedIntoWall reports a defender whose own pushback drove them into the wall
// that correction d is bringing them back out of.
//
// Sign-opposed, because a correction points back the way the velocity came. The
// state test is what keeps the rule to pushback: a player who simply walked
// into the corner has been clamped there since the game began and must not
// shove their opponent across the stage for it.
func pushedIntoWall(p *PlayerState, d Fix) bool {
	if !stunned(p.State) {
		return false
	}
	return (p.VX > 0 && d < 0) || (p.VX < 0 && d > 0)
}

// juggled reports a move that must refuse to connect because the defender has
// taken all the juggle hits it allows.
//
// Only in the air: on the ground the counter is zero and a limit of zero would
// otherwise refuse every hit in the game. Nil is the projectile's caller asking
// about a move that has gone; there is nothing to refuse.
func (s *GameState) juggled(defender int, mv *Move) bool {
	if mv == nil {
		return false
	}
	p := &s.Players[defender]
	return p.Airborne() && p.Juggle >= mv.JuggleLimitOf()
}

// gravity is the fall this player takes this frame: the character's own,
// increased with every juggle hit so an air combo self-terminates. The design
// note asks for exactly this — the opponent falls faster each hit, and the
// combo runs out of altitude before it runs out of moves.
//
// A percentage of the character's own gravity rather than a flat addition:
// gravity is a fraction of a unit per frame, and a balance file that has to
// express one is a balance file with a float in it.
func (p *PlayerState) gravity() Fix {
	g := CharacterAt(p.Char).Gravity
	if p.Juggle == 0 {
		return g
	}
	return g + Fix(int64(g)*int64(p.Juggle*balance.JuggleGravityPercent)/100)
}
