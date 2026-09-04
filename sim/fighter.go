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
)

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

	// Stun and commitment states tick down elsewhere; they accept no input.
	// StateAir is the exception, and only for buttons: an air normal is the one
	// thing a jump accepts. There is no air walking, no double jump and no air
	// dash, so the direction half below is unreachable from up there.
	if !Actionable(p.State) && p.State != StateAir && cancel == 0 {
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

	// Attacks first: a button beats a direction on the same frame, which is
	// what lets a crouching attack come out of a walk without a spare frame.
	if m := s.moveFor(i, stance, now, cancel); m >= 0 {
		p.enterMove(m, now)
		return
	}

	// A cancel window is not an actionable state. Nothing below this line — no
	// dash, no jump, no walk — comes out of the middle of an attack; the window
	// exists for the one move the data named and for nothing else.
	if p.State == StateAttack {
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

	if p.State == StateAir {
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
func (s *GameState) moveFor(i int, stance int32, now uint32, cancel uint16) int32 {
	p := &s.Players[i]
	c := CharacterAt(p.Char)

	// Candidates are ranked press frame first, special over normal second, and
	// the whole comparison is one integer: two ranks per frame, the odd one
	// taken by the special. A newer press therefore always outranks an older
	// one whatever it was, which is the ordering in words and cheaper to read
	// than the three-way condition it replaces.
	//
	// Starting the bar at p.Eaten*2+1 is what spends a press: nothing at or
	// before that frame can outrank it, special or not.
	best, bestRank := int32(-1), p.Eaten*2+1

	for m := int32(0); m < c.NumMoves; m++ {
		mv := &c.Moves[m]
		special := mv.Motion != MotionNone

		// A cancel takes only what the move being cancelled named. The category
		// is the move's own nature — a motion makes it a special, its absence
		// makes it a chain — so the target needs no field of its own to say
		// what it is.
		if cancel != 0 {
			cat := CancelChain
			if special {
				cat = CancelSpecial
			}
			if cancel&cat == 0 {
				continue
			}
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
		if special && s.MotionAt(i, uint32(f)) != mv.Motion {
			continue
		}

		rank := f * 2
		if special {
			rank++
		}
		if rank > bestRank {
			best, bestRank = m, rank
		}
	}
	return best
}

// advanceState is step 2: run the current state's clock and decide what the
// player's velocity is this frame.
func (s *GameState) advanceState(i int) {
	p := &s.Players[i]
	c := CharacterAt(p.Char)

	// Every state below sets its velocity from scratch each frame — except an
	// attack, which was handed its velocity when it started and keeps it. That
	// is what makes a launch an arc rather than a single frame of movement.
	if p.State != StateAttack {
		p.VX = 0
	}

	switch p.State {
	case StateWalkF:
		p.VX = c.WalkForward.Mul(FromInt(int(p.Facing)))

	case StateWalkB:
		p.VX = -c.WalkBack.Mul(FromInt(int(p.Facing)))

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

	case StateHitstun, StateBlockstun, StateLanding:
		if p.Stun > 0 {
			p.Stun--
		}
		if p.Stun == 0 {
			p.enter(StateIdle)
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
