package sim

// Input bitfield — one uint16 per player per frame. The same bytes go to the
// sim, the network, the replay and the server. Bits 7, 11-15 are reserved.
// SOCD is resolved client-side before packing; the sim trusts what it gets.
const (
	InUp    uint16 = 1 << 0
	InDown  uint16 = 1 << 1
	InLeft  uint16 = 1 << 2
	InRight uint16 = 1 << 3
	InLP    uint16 = 1 << 4
	InMP    uint16 = 1 << 5
	InHP    uint16 = 1 << 6
	InLK    uint16 = 1 << 8
	InMK    uint16 = 1 << 9
	InHK    uint16 = 1 << 10
)

// Stage geometry. Not character data — it belongs to the stage, and there is
// one stage.
const (
	GroundY        = Fix(0)
	StageHalfWidth = Fix(200) << FracBits // ~400 units wide

	// Half the view width in game units. The camera keeps this much visible on
	// each side of its centre and never shows past a wall.
	CameraHalfWidth = Fix(200) << FracBits
)

// PlayerState is one fighter. Every field is 4 bytes — see the note on
// GameState.
type PlayerState struct {
	X, Y   Fix
	VX, VY Fix

	// Facing is +1 (right) or -1 (left). Every motion is read relative to it —
	// quarter-circle forward is ↓↘→ facing right and ↓↙← facing left, and the
	// player pressed the same thing both times. Maintained by updateFacing.
	Facing int32

	// Char indexes the loaded roster.
	Char int32

	Health int32

	// State and StateFrame are the state machine. StateFrame counts from 0 on
	// the frame the state was entered.
	State      int32
	StateFrame int32

	// MoveIndex is the active move while State is StateAttack, else -1.
	MoveIndex int32

	// HasHit marks a move that has already connected, so one active window
	// cannot hit twice. Cleared when the move starts.
	HasHit int32

	// Stun is the remaining hitstun, blockstun or landing recovery. One
	// counter, because all three are the same thing to the state machine: a
	// fixed number of frames that accept no input.
	Stun int32

	// Landing is the recovery this player owes the moment they touch down —
	// the debt of a move that ended while they were still in the air, carried
	// because the move itself is gone by then. Cleared by every state change,
	// so being hit out of the fall cancels it.
	Landing int32

	// Eaten is the newest frame whose button presses have already been spent,
	// or -1. The input buffer keeps a press live for a few frames looking for a
	// state that can act on it (see moveFor); without a record of which presses
	// were already spent, the same tap keeps matching for the rest of the
	// window and one press produces a move on every actionable frame.
	//
	// In the state, not beside it, for the usual reason: a buffer that does not
	// roll back replays the wrong move.
	Eaten int32

	// JumpVX is the horizontal velocity committed at pre-jump. There is no air
	// control, so the whole arc follows from this and gravity.
	JumpVX Fix

	// Inputs is the input history ring: Inputs[f%InputHistory] is the bitfield
	// that advanced frame f, widened to uint32 to keep GameState padding-free.
	//
	// In the state, not beside it, because motion recognition reads it: history
	// outside GameState means a rollback replays a fireball as a crouch.
	Inputs [InputHistory]uint32
}

// GameState is all gameplay state. Flat, fixed-size, comparable: no pointers,
// no slices, no maps. Every field is 4-byte, so the struct has no implicit
// padding and no uninitialised bytes to leak into a checksum.
//
// **Every field must stay 4 bytes wide and 4-byte aligned.** That is what keeps
// the struct padding-free, which is what makes hashing its raw memory sound —
// see Checksum. A narrower field costs less space than the padding it forces,
// and TestGameStateHasNoImplicitPadding fails the moment one is added.
//
// If it is not in here, it does not roll back, and it is a bug. Camera, RNG,
// input history, AI state, timers and round transitions all belong here.
type GameState struct {
	Frame uint32
	// RNG is the whole generator — see rand.go. It rolls back because it is a
	// field, and for no other reason.
	RNG uint32

	// Hitstop freezes both fighters for a few frames on a connect. It is not
	// cosmetic: it changes when the next action can happen, so it is in the
	// sim and it is in the state.
	Hitstop int32

	// CamX is the camera centre. **In the state, because corner position is
	// gameplay** — a desynced camera is a desynced corner, and the corner is
	// where a large part of the game happens.
	CamX Fix

	Players [2]PlayerState

	// Projectiles is the pool. In the state for the same reason as everything
	// else in here: it rolls back, or a fireball that was rewound keeps flying
	// on one machine only.
	Projectiles [MaxProjectiles]Projectile
}

// New returns the starting state with both players on character 0.
func New() GameState { return NewMatch(0, 0) }

// NewMatch starts a match between two roster entries: players apart, on the
// ground, facing each other, at full health.
func NewMatch(c0, c1 int32) GameState {
	s := GameState{RNG: seed, Players: [2]PlayerState{
		{X: Fix(-60) << FracBits, Facing: 1, Char: c0, Health: CharacterAt(c0).Health},
		{X: Fix(60) << FracBits, Facing: -1, Char: c1, Health: CharacterAt(c1).Health},
	}}
	for i := range s.Players {
		s.Players[i].MoveIndex = -1
		s.Players[i].Eaten = -1
	}
	s.updateCamera()
	return s
}

// Advance runs one frame.
//
// The nine steps below are the update order, and the order is itself part of
// the spec — 02 Architecture/Deterministic Simulation.md. It is fixed, it is
// documented, and it is never reordered casually: two machines that run these
// steps in different orders produce different states from identical inputs,
// which is a desync with no other symptom.
func (s *GameState) Advance(in [2]uint16) {
	// 1. Resolve inputs. Record history first: motion recognition, the dash
	// double-tap and the input buffer all read the ring, and all must see this
	// frame.
	for i := range s.Players {
		s.Players[i].Inputs[s.Frame%InputHistory] = uint32(in[i])
	}

	// Hitstop freezes the whole match: no input, no movement, no new hits.
	// Only the counter runs, and the frame still advances so the checksum and
	// the network keep moving.
	if s.Hitstop > 0 {
		s.Hitstop--
		s.Frame++
		return
	}

	for i := range s.Players {
		s.resolveInputs(i, in[i])
	}

	// 2. Advance state machines.
	for i := range s.Players {
		s.advanceState(i)
	}

	// 3. Movement and gravity. Semi-implicit Euler: velocity first, then
	// position, so a landing is detected on the frame it happens.
	for i := range s.Players {
		p := &s.Players[i]

		// Whether the player was in the air is decided before the position
		// moves, and both the gravity and the landing below read that one
		// answer. Asking again afterwards would ask about a different frame.
		air := p.Airborne()
		if air {
			p.VY += CharacterAt(p.Char).Gravity
		}
		p.X += p.VX
		p.Y += p.VY

		if p.Y <= GroundY {
			p.Y = GroundY
			p.VY = 0
			if air {
				// Landing kills horizontal momentum, so a move that came down
				// does not slide through its recovery. Only for something that
				// was actually airborne: a grounded advancing move is on the
				// ground every frame and must keep the velocity its data gave
				// it.
				p.VX = 0

				// Landing ends a jump, and it ends an air normal with it: an
				// air move's recovery is the fall, and it has no business
				// continuing on the ground. A move that *launched* from the
				// ground — an uppercut — keeps its recovery, because those
				// frames are the punish window that makes it a risk.
				//
				// Either way the character owes what the move that put them up
				// there said they owe. Zero for a plain jump; without a value
				// for the rest, a whiffed uppercut that expires overhead and an
				// air normal held to the floor are both actionable on the frame
				// they touch it, which is a free reversal and a free jump-in.
				mv := p.move()
				switch {
				case mv != nil && mv.Stance == StanceAir:
					p.land(mv.Landing)
				case p.State == StateAir:
					p.land(p.Landing)
				}
			}
		}
	}

	// 4. Pushboxes and stage bounds, then turn to face the opponent.
	s.separate()
	s.clampToStage()
	s.updateFacing()

	// 5. Projectiles: they move before hit detection reads their boxes, and
	// after the players have been separated and clamped, so a fireball is
	// tested against final positions like everything else.
	s.advanceProjectiles()

	// 6. Hit detection, then 7. hit resolution. **Player 1's hitboxes against
	// player 2's hurtboxes first, then the reverse.** Both are collected before
	// either is applied, so a trade resolves identically on both machines
	// rather than depending on which player the loop reached first.
	//
	// The moves are captured alongside the hits, and for the same reason:
	// resolving player 0's hit puts player 1 in hitstun, which ends player 1's
	// attack — so by the time the second resolution ran, the move that was
	// about to land no longer existed and the trade silently became a
	// one-sided hit. Character data is immutable, so these pointers stay valid.
	mv0, mv1 := s.Players[0].move(), s.Players[1].move()
	hit0 := s.connects(0, 1)
	hit1 := s.connects(1, 0)
	if hit0 {
		s.resolveHit(0, 1, mv0, in[1])
	}
	if hit1 {
		s.resolveHit(1, 0, mv1, in[0])
	}

	// Projectiles resolve after both players, so a fireball and the punch that
	// beat it to the same frame trade in a fixed order rather than in whichever
	// order the loop happened to reach them.
	s.resolveProjectiles(in)

	// 8. Timers, resources, round state — round flow is M2. The camera is here
	// because it is derived from positions, which are final by now.
	s.updateCamera()

	// 9. Increment frame. The caller takes the checksum; the sim does not
	// store it, because a stored checksum would be state covering itself.
	s.Frame++
}

// connects reports whether attacker's hitboxes overlap defender's hurtboxes.
//
// Detection is separated from resolution so that both directions are tested
// against the same positions. Resolving as we go would let player 0's hit push
// player 1 out of range before player 1's hit is tested, and the trade would
// stop being a trade.
func (s *GameState) connects(attacker, defender int) bool {
	var hits, hurts [MaxBoxes]Box
	nh := s.Hitboxes(attacker, &hits)
	if nh == 0 {
		return false
	}
	nd := s.Hurtboxes(defender, &hurts)

	for a := int32(0); a < nh; a++ {
		for d := int32(0); d < nd; d++ {
			if hits[a].Overlaps(hurts[d]) {
				return true
			}
		}
	}
	return false
}

// resolveHit applies one connect. mv is the attacker's move as captured before
// any resolution ran; defenderIn is the defender's input this frame, which is
// what decides whether they were holding back.
func (s *GameState) resolveHit(attacker, defender int, mv *Move, defenderIn uint16) {
	ap := &s.Players[attacker]

	if mv == nil {
		return
	}
	ap.HasHit = 1
	s.applyHit(defender, mv, defenderIn)
}

// applyHit is the half of a connect that lands on the defender: block or hit,
// stun, damage, hitstop. Split out because a projectile connects without any
// attacker to mark — the fireball's owner may have recovered and walked away
// frames ago, and marking their current move as having hit would disable a
// hitbox they are holding out right now.
func (s *GameState) applyHit(defender int, mv *Move, defenderIn uint16) {
	dp := &s.Players[defender]

	if s.blocking(defender, defenderIn, mv.Level) {
		dp.enter(StateBlockstun)
		dp.Stun = mv.Blockstun
		// ponytail: no chip damage. It only exists during Burnout, and Burnout
		// is the Drive system, which is M2.
	} else {
		dp.enter(StateHitstun)
		dp.Stun = mv.Hitstun
		dp.Health -= mv.Damage
		if dp.Health < 0 {
			dp.Health = 0
		}
		// ponytail: no damage scaling, no juggles, no counter hits. Combo
		// scaling is M2 and needs a combo counter to scale against.
	}

	// Hitstop is one value for the match, not one per player: both fighters
	// freeze together, which is the whole effect. A trade takes the larger.
	if mv.Hitstop > s.Hitstop {
		s.Hitstop = mv.Hitstop
	}
}

// separate pushes overlapping pushboxes apart, half the overlap each, then the
// stage clamp runs after. A player pinned against a wall would otherwise be
// pushed through it.
func (s *GameState) separate() {
	a, b := s.Pushbox(0), s.Pushbox(1)

	// Vertical miss: one player is cleanly above the other, so they pass.
	if a.Y >= b.Y+b.H || b.Y >= a.Y+a.H {
		return
	}

	overlap := min(a.X+a.W, b.X+b.W) - max(a.X, b.X)
	if overlap <= 0 {
		return
	}

	// The tie at identical positions breaks by player index, which is the only
	// property that matters: it breaks the same way on every machine.
	push := overlap / 2
	if s.Players[0].X <= s.Players[1].X {
		s.Players[0].X -= push
		s.Players[1].X += push
	} else {
		s.Players[0].X += push
		s.Players[1].X -= push
	}
}

// clampToStage keeps both pushboxes inside the walls.
func (s *GameState) clampToStage() {
	for i := range s.Players {
		p := &s.Players[i]
		box := s.Pushbox(i)
		if d := -StageHalfWidth - box.X; d > 0 {
			p.X += d
		}
		if d := (box.X + box.W) - StageHalfWidth; d > 0 {
			p.X -= d
		}
	}
}

// updateFacing turns each player toward the other.
//
// Facing is not cosmetic. Everything facing-relative inverts with it: walk
// directions, motion recognition, which way boxes mirror, and — the one that
// bites — which direction is "away" and therefore blocks. A player who has been
// crossed up and still faces the old way cannot block at all.
//
// Only actionable grounded states turn. You do not pivot in the middle of an
// attack, in hitstun, during a dash, or in the air: keeping your facing through
// the whole jump is exactly what makes a crossup a crossup.
//
// Run after positions are final and before hit detection, so an attacker whose
// opponent just jumped over them still has boxes on the side they committed to,
// and the defender's hurtbox is already on the correct side.
func (s *GameState) updateFacing() {
	for i := range s.Players {
		p := &s.Players[i]
		if !Actionable(p.State) {
			continue
		}
		switch other := s.Players[1-i].X; {
		case other > p.X:
			p.Facing = 1
		case other < p.X:
			p.Facing = -1
			// Exactly level: keep the current facing. Picking a side here would
			// flip both players back and forth on a shared pixel, and it would
			// have to pick the same one on both machines for no benefit.
		}
	}
}

// updateCamera centres the camera between the players and keeps it inside the
// stage. Derived from positions and stored, so it is identical on both machines
// and rolls back with everything else.
func (s *GameState) updateCamera() {
	mid := (s.Players[0].X + s.Players[1].X) / 2

	// Never show past a wall: the corner has to look like a corner.
	if lo := -StageHalfWidth + CameraHalfWidth; mid < lo {
		mid = lo
	}
	if hi := StageHalfWidth - CameraHalfWidth; mid > hi {
		mid = hi
	}
	// A stage narrower than the view pins the camera at the centre rather than
	// letting the two clamps fight.
	if CameraHalfWidth >= StageHalfWidth {
		mid = 0
	}
	s.CamX = mid
}
