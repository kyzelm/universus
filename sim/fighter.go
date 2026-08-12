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
)

// Airborne reports whether a state is off the ground.
func Airborne(state int32) bool { return state == StateAir }

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

// enterMove starts an attack.
func (p *PlayerState) enterMove(index int32) {
	p.enter(StateAttack)
	p.MoveIndex = index
	p.HasHit = 0
}

// resolveInputs is step 1: turn this frame's bitfield into a state transition.
//
// Hitstop freezes both fighters mid-hit, so nothing here runs during it — that
// is checked by the caller, not by every branch below.
func (s *GameState) resolveInputs(i int, in uint16) {
	p := &s.Players[i]
	c := CharacterAt(p.Char)
	now := s.Frame

	// Stun and commitment states tick down elsewhere; they accept no input.
	if !Actionable(p.State) {
		return
	}

	dir := direction(in, p.Facing)
	crouching := dir == DirDown || dir == DirDownBack || dir == DirDownFwd

	// Attacks first: a button beats a direction on the same frame, which is
	// what lets a crouching attack come out of a walk without a spare frame.
	if m := s.moveFor(i, in, crouching, now); m >= 0 {
		p.enterMove(m)
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

// moveFor finds the move a freshly pressed button selects, or -1.
//
// Scanning the move list in index order — the same order on every machine. The
// first match wins, so a character's move list is authored most-specific first.
func (s *GameState) moveFor(i int, in uint16, crouching bool, now uint32) int32 {
	p := &s.Players[i]
	c := CharacterAt(p.Char)

	stance := int32(StanceStand)
	if crouching {
		stance = StanceCrouch
	}

	for m := int32(0); m < c.NumMoves; m++ {
		mv := &c.Moves[m]
		if mv.Stance != stance {
			continue
		}
		// The press edge, not the held bit: a held button must not re-fire its
		// move on every actionable frame.
		if in&mv.Button != 0 && p.at(now, 1)&mv.Button == 0 {
			return m
		}
	}
	return -1
}

// advanceState is step 2: run the current state's clock and decide what the
// player's velocity is this frame.
func (s *GameState) advanceState(i int) {
	p := &s.Players[i]
	c := CharacterAt(p.Char)

	p.VX = 0

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
			p.enter(StateIdle)
			return
		}

	case StateHitstun, StateBlockstun:
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
		if k := mv.BoxesAt(p.StateFrame); k != nil {
			for b := int32(0); b < k.NumHurt; b++ {
				out[b] = k.Hurt[b].World(p.X, p.Y, p.Facing)
			}
			return k.NumHurt
		}
	}

	var local Box
	switch {
	case Airborne(p.State):
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
	if Airborne(p.State) || p.State == StatePreJump {
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
