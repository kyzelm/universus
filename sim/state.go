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
}

// GameState is all gameplay state. Flat, fixed-size, comparable: no pointers,
// no slices, no maps. Every field is 4-byte, so the struct has no implicit
// padding and no uninitialised bytes to leak into a checksum.
//
// If it is not in here, it does not roll back, and it is a bug.
type GameState struct {
	Frame   uint32
	Players [2]PlayerState
}

// New returns the starting state: two players apart, on the ground.
func New() GameState {
	return GameState{Players: [2]PlayerState{
		{X: Fix(-60) << FracBits},
		{X: Fix(60) << FracBits},
	}}
}

// Advance runs one frame. The order is part of the spec — documented in
// 02 Architecture/Deterministic Simulation.md, never reordered casually.
// M0 implements steps 1, 3, 4 and 9; the rest have nothing to do yet.
func (s *GameState) Advance(in [2]uint16) {
	// 1. Resolve inputs.
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

	// 5-8. Projectiles, hit detection, hit resolution, timers — M1/M2.

	// 9. Increment frame. Checksum lands here in step 3 of the plan.
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
