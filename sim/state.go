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

// ponytail: M0 hardcodes these. M1 moves them to character JSON and go:embeds
// it, per the no-hardcoded-gameplay-values invariant — this spike is throwaway.
const (
	Gravity      = Fix(-24576) // -0.375 units/frame^2 -> ~43 frame jump
	WalkSpeed    = Fix(98304)  // 1.5 units/frame
	JumpVelocity = Fix(524288) // 8.0 units/frame

	GroundY         = Fix(0)
	StageHalfWidth  = Fix(200) << FracBits // stage is ~400 units wide
	PlayerHalfWidth = Fix(12) << FracBits
	PlayerHeight    = Fix(48) << FracBits
)

// PlayerState is a position and a velocity. Y is the feet; up is positive.
type PlayerState struct {
	X, Y   Fix
	VX, VY Fix

	// Facing is +1 (right) or -1 (left). Every motion is read relative to it —
	// quarter-circle forward is ↓↘→ facing right and ↓↙← facing left, and the
	// player pressed the same thing both times.
	//
	// ponytail: set once in New and never updated. Turning to face the opponent
	// belongs to the state machine (step 2), which decides when a turn is
	// allowed — you do not pivot mid-move. Correct for the starting positions
	// until then.
	Facing int32

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
	RNG     uint32
	Players [2]PlayerState
}

// New returns the starting state: two players apart, on the ground, facing each
// other, RNG seeded.
func New() GameState {
	return GameState{RNG: seed, Players: [2]PlayerState{
		{X: Fix(-60) << FracBits, Facing: 1},
		{X: Fix(60) << FracBits, Facing: -1},
	}}
}

// Advance runs one frame.
//
// The nine steps below are the update order, and the order is itself part of
// the spec — 02 Architecture/Deterministic Simulation.md. It is fixed, it is
// documented, and it is never reordered casually: two machines that run these
// steps in different orders produce different states from identical inputs,
// which is a desync with no other symptom.
//
// Every step keeps its numbered slot even while empty, so filling one in is an
// edit inside a slot rather than a decision about where it goes.
//
//	1. resolve inputs      SOCD is already resolved client-side; buffer and
//	                       motion recognition land here (M1)
//	2. state machines      per-player action frames (M1)
//	3. movement, gravity   done
//	4. pushboxes, bounds   done
//	5. projectiles         (M2)
//	6. hit detection       P1 hitboxes vs P2 hurtboxes FIRST, then the reverse.
//	                       The fixed order is what makes a trade resolve
//	                       identically on both machines (M1)
//	7. hit resolution      damage, scaling, hitstun, meter (M1/M2)
//	8. timers, round state (M2)
//	9. increment frame     done; the checksum is taken by the caller
func (s *GameState) Advance(in [2]uint16) {
	// 1. Resolve inputs. Record history first: motion recognition and the input
	// buffer both read the ring, and both must see this frame.
	for i := range s.Players {
		s.Players[i].Inputs[s.Frame%InputHistory] = uint32(in[i])
	}

	for i := range s.Players {
		p := &s.Players[i]
		p.VX = 0
		if in[i]&InLeft != 0 {
			p.VX -= WalkSpeed
		}
		if in[i]&InRight != 0 {
			p.VX += WalkSpeed
		}
		if in[i]&InUp != 0 && p.Y == GroundY {
			p.VY = JumpVelocity
		}
	}

	// 2. Advance state machines — M1.

	// 3. Movement and gravity. Semi-implicit Euler: velocity first, then
	// position, so a landing is detected on the frame it happens.
	for i := range s.Players {
		p := &s.Players[i]
		p.VY += Gravity
		p.X += p.VX
		p.Y += p.VY
		if p.Y <= GroundY {
			p.Y = GroundY
			p.VY = 0
		}
	}

	// 4. Pushboxes and stage bounds.
	s.separate()
	for i := range s.Players {
		p := &s.Players[i]
		if p.X < -StageHalfWidth+PlayerHalfWidth {
			p.X = -StageHalfWidth + PlayerHalfWidth
		}
		if p.X > StageHalfWidth-PlayerHalfWidth {
			p.X = StageHalfWidth - PlayerHalfWidth
		}
	}

	// 5. Projectiles — M2.

	// 6. Hit detection — M1. P1's hitboxes vs P2's hurtboxes first, then the
	// reverse. The order is not an implementation detail; see the doc comment.

	// 7. Hit resolution: damage, scaling, hitstun, meter — M1/M2.

	// 8. Timers, resources, round state — M2.

	// 9. Increment frame. The caller takes the checksum; the sim does not
	// store it, because a stored checksum would be state covering itself.
	s.Frame++
}

// separate pushes overlapping pushboxes apart, half the overlap each.
// ponytail: runs before the wall clamp, so a player pinned in a corner can be
// pushed back into the wall and stay overlapped for a frame. M1 resolves
// against the wall properly; for M0 the rectangles just need to not merge.
func (s *GameState) separate() {
	a, b := &s.Players[0], &s.Players[1]

	// dx >= 0 when b is to the right of a. At dx == 0 the tie breaks the same
	// way on every machine, which is the only property that matters here.
	dx := b.X - a.X
	overlap := PlayerHalfWidth*2 - dx.Abs()
	if overlap <= 0 {
		return
	}
	if a.Y >= b.Y+PlayerHeight || b.Y >= a.Y+PlayerHeight {
		return
	}

	push := overlap / 2
	if dx >= 0 {
		a.X -= push
		b.X += push
	} else {
		a.X += push
		b.X -= push
	}
}
