package sim

// The character state machine. Every fighter is in exactly one state per frame,
// and state plus StateFrame determines everything: which boxes are active, what
// input is accepted, what happens next.
//
// The sim decides transitions. The view reads the state and draws it; it never
// picks one.

// States. Values are part of the state's byte layout, so they are appended to,
// never reordered.
const (
	StateIdle int32 = iota
	StateWalkF
	StateWalkB
	StateCrouch
	StateDash
	StateBackdash
	StatePreJump // grounded startup — this is what makes throws beat jumps
	StateAir
	StateAttack
	StateHitstun
	StateBlockstun
	// StateLanding is the recovery owed for coming down out of a move. It is
	// stun as far as the state machine is concerned — a fixed count of frames
	// that accept nothing — so it runs on the same counter.
	StateLanding
	// StateThrown is being thrown, and it is also the recovery both players owe
	// after a throw is teched. The same counter again: from the state machine's
	// side every one of these is a fixed number of frames that accept nothing,
	// and the difference between them is who is embarrassed.
	StateThrown
	// StateKnockdown is lying on the ground after a sweep or a throw, and it is
	// the state okizeme is played against: the attacker knows exactly which
	// frame the defender rises on, and that shared knowledge is the mechanic.
	//
	// **Invulnerable for the whole of it**, so the defender becomes vulnerable
	// and actionable on the same frame. Any overlap between the two would be
	// frames on which they can be hit and cannot block, and an attack timed
	// into that gap knocks them down again — a loop nobody can escape. A meaty
	// is an attack whose active frames cover the first *actionable* frame, and
	// that is exactly the attack this leaves possible.
	StateKnockdown
	// StateParry is the Drive Parry stance: held rather than pressed, draining
	// Drive for as long as it is, and absorbing what lands on it **without
	// blockstun** — which is the whole mechanic. Blocking keeps the defender
	// frozen for the attacker's next hit; parrying gives the frames back, so
	// the pressure ends rather than continuing.
	//
	// It is a state and not a flag because it is exclusive: a parrying player
	// is not walking, blocking or attacking, and the thing that ends it is
	// letting go.
	StateParry
	// StateRush is Drive Rush: a forward dash the player can act out of. That
	// is the whole mechanic — an ordinary dash commits and a rush does not, so
	// it converts a poke into a combo and a blocked normal into pressure.
	//
	// It is a commitment for *directions* and not for buttons, exactly like
	// StateAir: there is no steering a rush, and cancelling it into an attack
	// is what it is for.
	StateRush
)

// ParryButtons is the Drive Parry input: medium punch and medium kick together,
// where the genre puts it. Held, not pressed — there is no edge here, because
// the mechanic is the holding.
const ParryButtons = InMP | InMK

// absorbs reports whether this player's current move eats the hit about to
// land on them: it has armour left, and it is still on the frames that carry
// it. The recovery is deliberately exposed — armour that lasted the whole move
// would make the move safe as well as strong.
func (p *PlayerState) absorbs() bool {
	mv := p.move()
	return mv != nil && mv.Armored() && p.Armor < mv.Armor &&
		p.StateFrame < mv.Startup+mv.Active
}

// Airborne reports whether the player is off the ground.
//
// State alone stopped being enough the moment a move could carry the character
// upward: a rising uppercut is airborne without being in StateAir, and gravity,
// the landing check and the air hurtbox all have to agree about that. StateAir
// stays in the test because a jump's first frame is still at ground level and
// must keep behaving exactly as it did.
func (p *PlayerState) Airborne() bool { return p.State == StateAir || p.Y > GroundY }

// Actionable reports whether a state accepts a new action this frame. Dash and
// backdash commit; attacks, stun and pre-jump run to completion.
func Actionable(state int32) bool {
	switch state {
	case StateIdle, StateWalkF, StateWalkB, StateCrouch:
		return true
	}
	return false
}

// stunned reports the states that are a fixed count of frames accepting no
// input — hitstun, blockstun, landing recovery, a throw and its tech, and lying
// on the floor. One list, because three places ask the same question: what may
// be thrown, what keeps its pushback rather than having its velocity reset, and
// what the corner rule counts as being pushed.
func stunned(state int32) bool {
	switch state {
	case StateHitstun, StateBlockstun, StateLanding, StateThrown, StateKnockdown:
		return true
	}
	return false
}

// enter puts the player into a state, resetting its frame counter. Everything
// that changes state goes through here, so "state changed but the counter did
// not" cannot happen.
func (p *PlayerState) enter(state int32) {
	p.State = state
	p.StateFrame = 0
	p.MoveIndex = -1
	// The debt belongs to the move that incurred it. Anything that ends that
	// move early — most of all being hit out of it — cancels the debt with it,
	// or a player knocked out of an uppercut would land into recovery frames
	// they never earned on top of the hitstun they did.
	p.Landing = 0
	// The knockdown owed by the hit currently being served, cleared for the
	// same reason and by the same rule: being hit out of hitstun replaces that
	// hit's consequences with the new one's, so a sweep that was interrupted
	// does not still knock down at the end of somebody else's combo.
	p.Down = 0
}

// knockdown puts the player on the ground. **One duration for every knockdown
// in the game** — the design note is explicit that varying wakeup timings are a
// balance rabbit hole that buys the thesis nothing, so the number is one
// balance value and not a per-move field.
func (p *PlayerState) knockdown() {
	p.enter(StateKnockdown)
	p.Stun = balance.KnockdownFrames
	p.Events |= EventKnockdown
}

// land ends an airborne action on touchdown, owing n frames of recovery. Zero
// is the common case and means actionable immediately, which is what a jump
// with no attack in it does.
func (p *PlayerState) land(n int32) {
	if n <= 0 {
		p.enter(StateIdle)
		return
	}
	p.enter(StateLanding)
	p.Stun = n
}

// hurt takes n damage, floored at zero. One place, because chip damage and a
// clean hit must clamp the same way — a health value below zero is a bar that
// draws backwards and a round that ends twice.
func (p *PlayerState) hurt(n int32) {
	p.Health -= n
	if p.Health < 0 {
		p.Health = 0
	}
}

// stay is enter for a state the player may already be in: holding forward for
// twenty frames is one walk, not twenty. Re-entering every frame would pin
// StateFrame at zero and make "frames spent in this state" unusable for
// anything that later needs it.
func (p *PlayerState) stay(state int32) {
	if p.State != state {
		p.enter(state)
	}
}

// enterMove starts an attack on frame now, and spends every press up to and
// including that frame: the press that started this move must not start
// another one while it is still inside the buffer window.
//
// ponytail: one frame marker, not a per-button flag. Eating a simultaneous
// press of a different button is the coarse part, and it is the right coarse
// part — the newest press is the one the player meant, and a second live press
// under the first would come out as a move the player never asked for.
func (p *PlayerState) enterMove(index int32, now uint32) {
	p.enter(StateAttack)
	p.MoveIndex = index
	p.HasHit = 0
	p.Eaten = int32(now)

	// A grounded move owns the character's velocity from here. Two things
	// follow: a walk does not carry into the attack that came out of it, and a
	// move with a launch sets its own and keeps it, so gravity turns it into an
	// arc without the move having to describe one.
	//
	// An air move does not take the velocity over unless it asks to. The jump
	// it came out of continues underneath it, which is what makes an air normal
	// something you do during a jump rather than something that stops one.
	mv := &CharacterAt(p.Char).Moves[index]

	// The meter is spent here, on the frame the move starts, and nowhere else.
	// moveFor has already refused a super the player cannot afford, so this is
	// a deduction and not a check — one place that can take the bars, which is
	// what keeps "it came out" and "it was paid for" from ever disagreeing.
	p.Super -= mv.SuperCost()
	if mv.Super > 0 {
		p.Events |= EventSuper
	}

	// Drive is spent here for the same reason and in the same place. Spending
	// the last of it enters Burnout — spendDrive is what decides that — so a
	// mechanic can be paid for with the bar that burns the player out, which is
	// the gamble the gauge is supposed to offer.
	if cost := mv.DriveCost(); cost > 0 {
		p.spendDrive(cost)
	}

	// Armour is counted per move, so the count resets with the move rather than
	// carrying absorbed hits into the next one.
	p.Armor = 0

	if !p.Airborne() || mv.Launches() {
		p.VX = mv.LaunchVX.Mul(FromInt(int(p.Facing)))
		p.VY = mv.LaunchVY
	}
}

// resolveInputs is step 1: turn this frame's bitfield into a state transition.
//
// Hitstop freezes both fighters mid-hit, so nothing here runs during it — that
// is checked by the caller, not by every branch below.
func (s *GameState) resolveInputs(i int, in uint16) {
	p := &s.Players[i]
	c := CharacterAt(p.Char)
	now := s.Frame

	// A move that has connected stops being a commitment if its data says so:
	// the cancel window accepts one thing, a move in a category the move being
	// cancelled names. Zero everywhere else, which is every move that does not
	// cancel and every state that is not an attack.
	cancel := s.cancelMask(i)

	// **Drive Parry is a hold, so it is resolved before any button can be read
	// as a move.** MP+MK would otherwise select the medium punch on the frame
	// it is pressed and the parry would never come out, the same way LP+LK
	// would be a jab if the throw did not outrank it.
	if p.State == StateParry {
		// Forward, forward, out of the parry: the cheap way into a Drive Rush,
		// and the reason the stance is worth holding when nothing is coming —
		// a parry that only ever absorbed would be purely defensive.
		if s.startRush(i, now, balance.DriveRushCost) {
			return
		}
		// Nothing else comes out of a parry but letting go of it. Burnout ends
		// it too: the gauge emptying is what stops the stance, and the frame it
		// empties on is this one.
		if in&ParryButtons != ParryButtons || p.Burnout != 0 {
			p.enter(StateIdle)
		}
		return
	}
	if in&ParryButtons == ParryButtons &&
		(Actionable(p.State) || s.pairLate(i, in, ParryButtons)) &&
		!p.Airborne() && p.Burnout == 0 && p.Drive > 0 {
		p.enter(StateParry)
		// Spent for the same reason: the buttons that entered the stance must
		// not still be waiting in the buffer to become a medium punch on the
		// frame the parry is released.
		p.Eaten = int32(now)
		return
	}

	// Blockstun accepts exactly one thing: the Drive Reversal, two bars for an
	// invincible counter-attack out of the pressure (03 Game Design/Resource
	// System.md). Handled before the gate below rather than by making blockstun
	// actionable — actionable blockstun is no blockstun at all.
	if p.State == StateBlockstun {
		if m := s.moveFor(i, StanceStand, now, 0, true); m >= 0 {
			// The blockstun it is escaping is over. Nothing else in the game
			// leaves stun early, so the counter is cleared here rather than in
			// enter, where it would silently change every other transition.
			p.Stun = 0
			p.enterMove(m, now)
		}
		return
	}

	dir := direction(in, p.Facing)
	crouching := dir == DirDown || dir == DirDownBack || dir == DirDownFwd

	stance := int32(StanceStand)
	switch {
	// Airborne rather than StateAir, for the cancel window's sake: a rising
	// uppercut is off the ground without being in StateAir, and what it could
	// cancel into up there is air moves. Grounded states sit exactly on
	// GroundY, so nothing else reaching this line changes answer.
	case p.Airborne():
		stance = StanceAir
	case crouching:
		stance = StanceCrouch
	}

	// **The second button of a two-button input lands late, and the move it was
	// meant to be still comes out.** Nothing makes a human press two keys on
	// one frame; the faster one selects its own normal, the state stops being
	// actionable, and the throw, parry or Impact the player was making never
	// happens. Only a multi-button move may take over, only over a
	// single-button one it shares a button with, and only while that one is
	// still in startup — see pairLate.
	if p.State == StateAttack && !Actionable(p.State) {
		if m := s.moveFor(i, stance, now, 0, false); m >= 0 &&
			s.pairLate(i, in, CharacterAt(p.Char).Moves[m].Button) {
			p.enterMove(m, now)
			return
		}
	}

	// Stun and commitment states tick down elsewhere; they accept no input.
	// StateAir is the exception, and only for buttons: an air normal is the one
	// thing a jump accepts. There is no air walking, no double jump and no air
	// dash, so the direction half below is unreachable from up there.
	// StateRush joins StateAir as the state that takes buttons and no
	// directions: cancelling the rush into an attack is the mechanic, steering
	// it is not.
	if !Actionable(p.State) && p.State != StateAir && p.State != StateRush && cancel == 0 {
		return
	}

	// Attacks first: a button beats a direction on the same frame, which is
	// what lets a crouching attack come out of a walk without a spare frame.
	if m := s.moveFor(i, stance, now, cancel, false); m >= 0 {
		p.enterMove(m, now)
		return
	}

	// A cancel window is not an actionable state. Nothing below this line — no
	// dash, no jump, no walk — comes out of the middle of an attack; the window
	// exists for the one move the data named, for the Drive Rush the move named
	// with it, and for nothing else.
	if p.State == StateAttack {
		// **Three bars from a cancel against one from the parry.** The design
		// prices them apart because they buy different things: out of a
		// connected normal a rush is a combo, out of the stance it is
		// approach.
		if cancel&CancelDrive != 0 {
			s.startRush(i, now, balance.DriveRushCancelCost)
		}
		return
	}

	// Dashes: two taps of the same direction inside the dash window. Read off
	// the input history, so it rolls back with everything else.
	if p.doubleTapped(now, DirFwd) {
		p.enter(StateDash)
		return
	}
	if p.doubleTapped(now, DirBack) {
		p.enter(StateBackdash)
		return
	}

	if p.State == StateAir || p.State == StateRush {
		return
	}

	switch {
	case dir == DirUp || dir == DirUpFwd || dir == DirUpBack:
		p.enter(StatePreJump)
		// The jump direction is committed here, at the *start* of pre-jump.
		// There is no air control, so this is the only frame it can be chosen,
		// and committing early is what makes a jump a commitment.
		switch dir {
		case DirUpFwd:
			p.JumpVX = c.JumpForwardVX.Mul(FromInt(int(p.Facing)))
		case DirUpBack:
			p.JumpVX = -c.JumpForwardVX.Mul(FromInt(int(p.Facing)))
		default:
			p.JumpVX = 0
		}

	case crouching:
		p.stay(StateCrouch)

	case dir == DirFwd:
		p.stay(StateWalkF)

	case dir == DirBack:
		p.stay(StateWalkB)

	default:
		p.stay(StateIdle)
	}
}

// dashWindow is how close two taps must be to dash. A feel value.
const dashWindow = 10

// doubleTapped reports two clean presses of dir inside dashWindow.
//
// Reading history rather than storing a tap counter keeps the answer a function
// of state, which is what makes it survive a rollback.
//
// **Strict: the taps must be the pure direction and the gap must be neutral.**
// Anything else in the window ends the sequence — the opposite direction, a
// diagonal, up, down. A looser rule reads intent that was never there: treating
// any non-matching input as the gap makes back, forward, back a backdash, and
// allowing a diagonal in the gap makes back, down, back one too. Both fire
// while the player is doing something else entirely, and a dash you did not ask
// for in a fighting game is worse than one you have to ask for twice.
//
// The cost is that a sloppy input does not dash. On a keyboard, where pure
// directions are what the hardware produces anyway, that is the right trade.
func (p *PlayerState) doubleTapped(now uint32, dir uint8) bool {
	taps, released := 0, false

	for back := uint32(0); back < dashWindow && back <= now; back++ {
		switch d := direction(p.at(now, back), p.Facing); d {
		case dir:
			// Only a fresh press counts. Holding the direction down across
			// several frames is one tap, not one per frame.
			if back == 0 || released {
				taps++
				released = false
				if taps == 2 {
					return true
				}
			}

		case DirNeutral:
			released = true

		default:
			return false
		}
	}
	return false
}

// cancelMask is the set of categories the player's current move may be
// cancelled into this frame, or zero for no cancel.
//
// Two conditions, both standard, both load-bearing. **The move must have
// connected** — HasHit, which is set on block as much as on hit, so a blocked
// normal cancels and a whiffed one does not; a whiff cancel would make every
// cancelable button safe to throw out. And the window opens at the first active
// frame, so a cancel cannot come out before the move it is cancelling has had a
// chance to be one.
func (s *GameState) cancelMask(i int) uint16 {
	p := &s.Players[i]
	mv := p.move()
	if mv == nil || p.HasHit == 0 || p.StateFrame < mv.Startup {
		return 0
	}
	return mv.CancelInto
}

// moveFor finds the move a recent button press selects, or -1.
//
// **This is where the input buffer is consumed.** A press does not have to land
// on a frame the player happens to be actionable: it stays live for InputBuffer
// frames looking for a state that can act on it, so a jab pressed during
// hitstop, hitstun or the tail of another move comes out on the first frame it
// legally can instead of being dropped for being three frames early. Buffering
// is most of what "responsive" means in this genre, and every one of those
// frames is a frame the player was already committed to.
//
// The newest live press wins — the player's latest intention, not their oldest.
// Presses at or before p.Eaten are spent and cannot win, which is what makes
// one press one move; starting bestFrame at p.Eaten is the whole filter.
//
// Scanning the move list in index order — the same order on every machine — and
// strictly newer beats equal, so a tie between two moves on the same frame goes
// to the lower index. A character's move list is authored most-specific first.
//
// cancel restricts the search to the categories a cancel allows; zero is the
// ordinary path and allows every move.
//
// reversal swaps the search over to the moves that come out of blockstun and
// away from every move that does not. It is a swap rather than an addition in
// both directions: a Drive Reversal is not something to press in neutral, and
// nothing else may be pressed while blocking.
func (s *GameState) moveFor(i int, stance int32, now uint32, cancel uint16, reversal bool) int32 {
	p := &s.Players[i]
	c := CharacterAt(p.Char)

	// Candidates are ranked press frame first and move tier second — super over
	// special over normal — and the whole comparison is one integer: three
	// ranks per frame, one per tier. A newer press therefore always outranks an
	// older one whatever it was, which is the ordering in words and cheaper to
	// read than the nested conditions it replaces.
	//
	// Starting the bar at the top rank of p.Eaten's frame is what spends a
	// press: nothing at or before that frame can outrank it, at any tier.
	best, bestRank := int32(-1), p.Eaten*tiers+tiers-1

	for m := int32(0); m < c.NumMoves; m++ {
		mv := &c.Moves[m]
		special := mv.Motion != MotionNone

		if mv.IsReversal() != reversal {
			continue
		}

		// A cancel takes only what the move being cancelled named. The category
		// is the move's own nature — a level makes it a super, a motion makes
		// it a special, neither makes it a chain — so the target needs no field
		// of its own to say what it is.
		if cancel != 0 && cancel&mv.Category() == 0 {
			continue
		}

		// The meter gates the super, and it gates it *here*, at selection: a
		// level 3 the player cannot afford must not be picked and then fail,
		// because a picked move has already beaten every cheaper one that
		// shared the button. Refusing it in the search is what lets the fireball
		// come out of the same input instead.
		if cost := mv.SuperCost(); cost > p.Super {
			continue
		}

		// The Drive gauge gates its own moves the same way, and Burnout refuses
		// them outright: **no Drive mechanics at all while the gauge is
		// refilling** is the design's own rule, and it is what makes running
		// out of Drive a state worth avoiding rather than a slower meter. An EX
		// fireball the player cannot pay for therefore comes out as the plain
		// fireball sharing its motion, which is the same courtesy the super
		// gets.
		if cost := mv.DriveCost(); cost > 0 && (p.Burnout != 0 || cost > p.Drive) {
			continue
		}

		// Ground and air never mix, specials included: a fireball motion is
		// still on the stick when the character leaves the ground, and without
		// this a jump would throw one.
		if (mv.Stance == StanceAir) != (stance == StanceAir) {
			continue
		}

		// Beyond that, stance gates normals only. A special is identified by
		// its motion, and requiring the stance as well would make the exact
		// frame the button lands decide the move: press punch one frame early,
		// while ↘ is still held, and a fireball becomes a crouching kick.
		//
		// The stance for a normal is this frame's, not the buffered frame's:
		// holding down through a buffered jab gives the crouching attack, which
		// is what a player still holding down is asking for.
		if !special && mv.Stance != stance {
			continue
		}

		f := p.pressFrame(now, mv.Button)
		if f < 0 {
			continue
		}
		// The motion is checked at the frame of the *press*, not at the frame
		// the move finally comes out. A buffered special is one the player
		// completed several frames ago; asking whether the motion is still
		// recognised now would drop exactly the inputs the buffer exists for.
		//
		// satisfies rather than equality, because a double quarter-circle is a
		// quarter-circle that kept going: the same input offers the super and
		// the fireball, and which one comes out is decided by the tier below
		// and by whether the meter could pay for it.
		if special && !s.MotionAt(i, uint32(f)).satisfies(mv.Motion) {
			continue
		}

		rank := f*tiers + tierOf(mv)
		if rank > bestRank {
			best, bestRank = m, rank
		}
	}
	return best
}

// The selection tiers. A super outranks a Drive move outranks a special
// outranks a throw outranks a normal on the same press, which is what puts the
// level 3 ahead of the fireball when one input describes both.
//
// The throw's tier is what makes LP+LK a throw rather than a jab. Both moves
// see a fresh press on that frame — the jab's button is a subset of the
// throw's — and the more specific input has to win, or a character with a
// throw cannot press two buttons at once for anything else again.
//
// A Drive move sits above the special for exactly that reason: an EX fireball
// is the same motion on two buttons, one of which is the light version's, so
// the cheaper move would win every press if the tiers were equal. The gauge
// check above is what keeps it honest — a Drive move the player cannot afford
// never reaches the comparison at all.
const tiers = 5

func tierOf(m *Move) int32 {
	switch {
	case m.Super > 0:
		return 4
	case m.Drive > 0:
		return 3
	case m.Motion != MotionNone:
		return 2
	case m.IsThrow():
		return 1
	default:
		return 0
	}
}

// multiButton reports a button mask with more than one bit — a throw, a parry,
// a Drive Impact. Kernighan's trick: clearing the lowest set bit leaves nothing
// behind for a single button.
func multiButton(mask uint16) bool { return mask&(mask-1) != 0 }

// pairLate reports that the player is a frame or two into a single-button
// attack and is now completing the two-button input pair, which that attack's
// button is part of.
//
// **This reverses D89's first half.** That decision fixed the throw with a
// macro key and rejected a sim rule, on the grounds that the macro bought
// everything the rule would. Two mechanics arrived after it — Drive Parry and
// Drive Impact, both a punch and a kick — and playing it says otherwise: a
// stance that is *held* and that gates the Drive Rush is not a keypress, and a
// player who reaches for the two buttons gets a medium punch every time. The
// macros stay; this is the same leniency for everyone who does not use them.
//
// It is a simulation rule rather than a client one for the usual reason: two
// machines that disagreed about whether a throw came out have desynced. It
// reads the input history every other rule reads, so it rolls back with the
// rest of the state.
//
// Four conditions, each load-bearing:
//
//   - **Inside the window**, balance.PairFrames — and **still in startup**,
//     whichever ends first. A move that has reached its active frames is a
//     commitment; taking one back would be a retraction rather than room, and
//     the per-move half means the window can be tuned for the slowest hand
//     without ever reaching past the fastest jab.
//   - **The move has not connected.** HasHit is set on block as much as on hit,
//     so a jab that already touched somebody is never rewritten into a throw.
//   - **The pair contains the running move's button.** Without it this would be
//     a free cancel out of any light into anything with two buttons, for the
//     length of the window.
//   - **The pair is complete this frame.** The press edge for a mask already
//     says "all of them down now, not all of them down before"; this is the
//     state half of the same question.
func (s *GameState) pairLate(i int, in, pair uint16) bool {
	p := &s.Players[i]
	if !multiButton(pair) || in&pair != pair {
		return false
	}
	if p.State != StateAttack || p.HasHit != 0 || p.StateFrame >= balance.PairFrames {
		return false
	}
	mv := p.move()
	return mv != nil && !multiButton(mv.Button) && p.StateFrame < mv.Startup &&
		pair&mv.Button != 0
}

// startRush enters a Drive Rush if the player asked for one and can pay for it:
// forward tapped twice, the gauge able to cover cost, and not burnt out.
// Reports whether it started.
//
// The double tap is read off the input history like every other dash, so it
// rolls back with the rest of the state.
func (s *GameState) startRush(i int, now uint32, cost int32) bool {
	p := &s.Players[i]
	if p.Burnout != 0 || p.Drive < cost || !p.doubleTapped(now, DirFwd) {
		return false
	}

	p.spendDrive(cost)
	p.enter(StateRush)
	// **The presses that bought the rush are spent**, exactly as a move spends
	// the press that started it. Without this the parry's own MP+MK is still
	// live in the buffer, and the rush — which takes buttons — turns it into a
	// medium punch on its second frame. Every Drive Rush would come with a free
	// attack nobody asked for.
	p.Eaten = int32(now)
	return true
}

// advanceState is step 2: run the current state's clock and decide what the
// player's velocity is this frame.
func (s *GameState) advanceState(i int) {
	p := &s.Players[i]
	c := CharacterAt(p.Char)

	// Every state below sets its velocity from scratch each frame — except an
	// attack, which was handed its velocity when it started and keeps it (that
	// is what makes a launch an arc rather than one frame of movement), and
	// stun, which keeps the pushback the hit gave it.
	switch {
	case p.State == StateAttack:
		// Keeps whatever enterMove handed it.

	case stunned(p.State):
		// Pushback bleeds off on the ground and not in the air: friction is
		// what ends a slide, and a launched defender has none until they land.
		// Integer decay reaches exactly zero — Go truncates toward it — so the
		// slide terminates rather than creeping by one unit forever.
		//
		// Not on the frame the push was given (StateFrame 0, which hitstop
		// holds there until the freeze ends): friction bleeds a slide off, it
		// does not take a bite out of the impulse before it has moved anyone.
		if !p.Airborne() && p.StateFrame > 0 {
			p.VX = Fix(int64(p.VX) * int64(balance.KnockbackDecay) / 100)
		}

	default:
		p.VX = 0
	}

	switch p.State {
	case StateWalkF:
		p.VX = c.WalkForward.Mul(FromInt(int(p.Facing)))

	case StateWalkB:
		p.VX = -c.WalkBack.Mul(FromInt(int(p.Facing)))

	case StateRush:
		// Exit before applying velocity, like the dash above: a state that
		// lasts N frames must move on exactly N of them.
		if p.StateFrame >= balance.DriveRushFrames {
			p.enter(StateIdle)
			break
		}
		p.VX = balance.DriveRushSpeed.Mul(FromInt(int(p.Facing)))

	case StateDash, StateBackdash:
		frames, dist := c.DashFrames, c.DashDistance
		if p.State == StateBackdash {
			frames, dist = c.BackdashFrames, -c.DashDistance
		}
		// Exit before applying velocity, not after. A state that lasts N frames
		// must move on exactly N of them; checking afterwards spends one more.
		if p.StateFrame >= frames {
			p.enter(StateIdle)
			return
		}
		// Constant speed over the dash: distance divided by duration, computed
		// once per frame in fixed-point rather than stored, so a rollback into
		// the middle of a dash needs no extra state.
		p.VX = dist.Div(FromInt(int(frames))).Mul(FromInt(int(p.Facing)))

	case StatePreJump:
		// Grounded, motionless, and throwable. The whole point of the state.
		if p.StateFrame >= c.PreJumpFrames {
			p.enter(StateAir)
			p.VY = c.JumpVelocity
			p.VX = p.JumpVX
			return
		}

	case StateAir:
		// No air control: the arc was committed at pre-jump.
		p.VX = p.JumpVX

	case StateAttack:
		mv := p.move()
		if mv == nil || p.StateFrame >= mv.Total() {
			// A move that runs out while the character is still off the ground
			// hands over to StateAir, not to idle: an idle player is actionable,
			// and actionable in mid-air is a different game.
			//
			// The move is gone by the next frame, so what it owes on landing is
			// recorded now. Anything else would need the state machine to
			// remember which move a fall came out of, which is the same field
			// under a worse name.
			if p.Y > GroundY {
				p.JumpVX = p.VX // StateAir drives VX from this
				owed := mv.Landing
				p.enter(StateAir)
				p.Landing = owed
				return
			}
			p.enter(StateIdle)
			return
		}

	case StateHitstun, StateBlockstun, StateLanding, StateThrown, StateKnockdown:
		// **Hitstun taken in the air does not run out in the air.** A launched
		// defender stays helpless until they touch the floor, where the landing
		// turns it into a knockdown; the alternative is a juggle that ends with
		// an opponent who becomes actionable in mid-air, which is a different
		// game and was reachable by any air-to-air hit before launchers
		// existed.
		if p.State == StateHitstun && p.Airborne() {
			break
		}
		if p.Stun > 0 {
			p.Stun--
		}
		if p.Stun == 0 {
			// A hit that knocks down owes the knockdown at the end of its
			// hitstun, not instead of it: the defender is struck, held for the
			// move's frames, and only then goes to the floor. Carried as a debt
			// on the player because the move is gone by then — the same shape
			// as the landing recovery above it.
			if p.Down != 0 {
				p.knockdown()
			} else {
				p.enter(StateIdle)
			}
			return
		}
	}

	p.StateFrame++
}

// move is the player's current move, or nil.
func (p *PlayerState) move() *Move {
	if p.State != StateAttack || p.MoveIndex < 0 {
		return nil
	}
	c := CharacterAt(p.Char)
	if p.MoveIndex >= c.NumMoves {
		return nil
	}
	return &c.Moves[p.MoveIndex]
}

// Hurtboxes writes the player's active hurtboxes into out and returns how many.
// Attack states use the move's keyframes; everything else uses the stance box.
func (s *GameState) Hurtboxes(i int, out *[MaxBoxes]Box) int32 {
	p := &s.Players[i]
	c := CharacterAt(p.Char)

	// A player on the floor has no hurtboxes at all. See StateKnockdown: the
	// invulnerability runs to the last frame of it, so vulnerable and
	// actionable begin together.
	if p.State == StateKnockdown {
		return 0
	}

	if mv := p.move(); mv != nil {
		// An invulnerable frame has no hurtboxes at all, which is the whole
		// mechanism: this is the one funnel every attack and every projectile
		// asks, so a move that answers nothing here cannot be hit by anything.
		// It is also what the debug overlay draws, so the window is visible
		// without a second code path to disagree with this one.
		if mv.Invulnerable(p.StateFrame) {
			return 0
		}
		if k := mv.BoxesAt(p.StateFrame); k != nil {
			for b := int32(0); b < k.NumHurt; b++ {
				out[b] = k.Hurt[b].World(p.X, p.Y, p.Facing)
			}
			return k.NumHurt
		}
	}

	var local Box
	switch {
	case p.Airborne():
		local = c.AirHurt
	case p.State == StateCrouch:
		local = c.CrouchHurt
	default:
		local = c.StandHurt
	}
	if local.Empty() {
		return 0
	}
	out[0] = local.World(p.X, p.Y, p.Facing)
	return 1
}

// Hitboxes writes the player's active hitboxes into out and returns how many.
// A move that has already connected has none: that is what stops one active
// window from hitting twice.
func (s *GameState) Hitboxes(i int, out *[MaxBoxes]Box) int32 {
	p := &s.Players[i]

	mv := p.move()
	if mv == nil || p.HasHit != 0 {
		return 0
	}
	k := mv.BoxesAt(p.StateFrame)
	if k == nil {
		return 0
	}
	for b := int32(0); b < k.NumHit; b++ {
		out[b] = k.Hit[b].World(p.X, p.Y, p.Facing)
	}
	return k.NumHit
}

// Pushbox is the player's world-space pushbox.
func (s *GameState) Pushbox(i int) Box {
	p := &s.Players[i]
	return CharacterAt(p.Char).Pushbox.World(p.X, p.Y, p.Facing)
}

// Blocking reports whether the player is holding away from the opponent and is
// therefore blocking, given the attack level.
//
// Holding back *is* blocking — there is no block button. A player in an
// actionable grounded state who is holding away blocks anything their stance
// covers; airborne players cannot block at all.
func (s *GameState) blocking(i int, in uint16, level int32) bool {
	p := &s.Players[i]
	if p.Airborne() || p.State == StatePreJump {
		return false
	}
	if !Actionable(p.State) && p.State != StateBlockstun {
		return false
	}

	d := direction(in, p.Facing)
	if d != DirBack && d != DirDownBack {
		return false
	}
	crouching := d == DirDownBack

	switch level {
	case LevelHigh:
		return !crouching // standing blocks highs
	case LevelLow:
		return crouching // crouching blocks lows
	default:
		return true // mids are blocked either way
	}
}
