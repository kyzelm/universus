package sim

// Throws and throw tech (03 Game Design/Movement and Defense.md).
//
// A throw is what makes blocking a decision instead of an answer. Blocking
// beats every strike in the game, so without something that beats blocking the
// correct play is to hold back and wait; the throw is that something, and the
// tech is what stops it being an unanswerable one.
//
// The rock-paper-scissors the design asks for falls out of three rules and no
// more: a throw ignores blocking, a throw refuses anyone airborne — pre-jump is
// grounded, which is what makes throws beat jump attempts — and a throw already
// in progress can be teched by pressing throw within a few frames of it landing.

// throwable reports whether the defender can be thrown at all.
//
// Not airborne, and not in stun. **Stun grants throw immunity**, and it is the
// rule that keeps pressure honest: without it a blocked light leads to a throw
// nobody can contest, since the defender is still in blockstun and cannot act
// at the moment the throw would connect.
//
// Invulnerability needs no mention here. It is the absence of hurtboxes (D76)
// and this runs behind the same funnel, so a move that cannot be hit cannot be
// thrown either — one window, as that decision says.
func (s *GameState) throwable(i int) bool {
	p := &s.Players[i]
	return !p.Airborne() && !stunned(p.State)
}

// techFrame is the frame on which the defender pressed throw inside the tech
// window, or -1 for no escape.
//
// The window is scanned **backward from the connect**, which is what a tech
// actually is: the defender committed to a throw of their own and the two
// crossed. It is the same press edge the state machine uses, over its own
// window — a throw is two buttons, and pressing them a frame apart is normal,
// so the edge is "both down now, not both down before" (see pressed).
//
// A character with no throw of their own cannot tech, and that is the honest
// answer rather than a special case: the input does not exist for them.
func (s *GameState) techFrame(defender int, now uint32) int32 {
	mv := throwOf(s.Players[defender].Char)
	if mv == nil {
		return -1
	}
	return s.Players[defender].pressWithin(now, mv.Button, uint32(balance.ThrowTechFrames))
}

// throwOf is the character's throw, or nil. Scanned in index order, so it is
// the same move on every machine; the first one wins, and no character has two.
func throwOf(char int32) *Move {
	c := CharacterAt(char)
	for i := int32(0); i < c.NumMoves; i++ {
		if c.Moves[i].IsThrow() {
			return &c.Moves[i]
		}
	}
	return nil
}

// applyThrow resolves a throw that has connected. It replaces the block-and-hit
// path entirely: a throw cannot be blocked, so there is nothing to ask about
// holding back, and it cannot counter-hit, because the defender was not
// attacking — being throwable is most of what "not attacking" means here.
func (s *GameState) applyThrow(attacker, defender int, mv *Move, now uint32) {
	ap, dp := &s.Players[attacker], &s.Players[defender]

	if s.techFrame(defender, now) >= 0 {
		s.tech(attacker, defender)
		return
	}

	dp.enter(StateThrown)
	dp.Stun = mv.Hitstun
	// A throw ends on the floor if its data says so, which is the whole of what
	// changed when knockdowns arrived: the fixed count of helpless frames a
	// thrown player used to spend is now the throw's own animation, and the
	// knockdown after it is the same one a sweep gives.
	if mv.KnocksDown() {
		dp.Down = 1
	}

	// Through the same damage pipeline as everything else, so the scaling order
	// has one implementation (D79). A throw always opens its own combo — the
	// defender had to be actionable to be thrown, which means out of hitstun,
	// which means the previous combo has already ended — so the starter it
	// captures is its own.
	dp.ComboStarter = starterScale(mv)
	dp.Combo++
	dp.Counter = CounterNone

	before := dp.Health
	dp.hurt(scaledDamage(mv, dp.ComboStarter, dp.Combo, CounterNone))
	s.Dealt[attacker] += before - dp.Health

	ap.gainSuper(mv.Damage * balance.SuperDealtPercent / 100)
	dp.gainSuper(mv.Damage * balance.SuperTakenPercent / 100)

	if mv.Hitstop > s.Hitstop {
		s.Hitstop = mv.Hitstop
	}
	ap.HasHit = 1
}

// tech is the escape: both players push apart, neither takes damage, and both
// owe the same recovery.
//
// **Symmetric on purpose.** A tech that left either side at an advantage would
// make the throw either free or unusable, and the point of the mechanic is that
// contesting a throw resets the situation rather than winning it.
func (s *GameState) tech(attacker, defender int) {
	push := FromInt(int(balance.ThrowTechPush))

	for _, i := range [2]int{attacker, defender} {
		p := &s.Players[i]
		p.enter(StateThrown)
		p.Stun = balance.ThrowTechRecovery
		p.VX = 0
		p.VY = 0
	}

	// Apart along the axis they are actually on, not along either player's
	// facing: at the instant of a crossup the two disagree about which way
	// forward is, and pushing by facing would send them the same way.
	if s.Players[attacker].X <= s.Players[defender].X {
		push = -push
	}
	s.Players[attacker].X += push
	s.Players[defender].X -= push

	// The walls have the final say, as they do after any other movement — a
	// tech in the corner pushes the attacker out and leaves the cornered player
	// where they are.
	s.clampToStage()
}
