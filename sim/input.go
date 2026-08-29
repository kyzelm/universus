package sim

// Frame-accurate input interpretation. Everything here reads the input history
// ring in GameState, so every answer rolls back with the state that produced it.
//
// The deterministic boundary sits *before* this file: raw browser events, SOCD,
// negative edge and key rebinding are all resolved client-side, and the sim sees
// only the packed bitfield. Nothing below may depend on a local setting.

// InputHistory is the ring length, in frames. A power of two so the index is a
// mask rather than a divide, and comfortably longer than the widest motion
// window plus the input buffer.
const InputHistory = 32

// InputBuffer is how long a button press stays live looking for a state that
// can act on it. A feel value — it wants playtest tuning, and the mechanism
// does not care what it is set to.
const InputBuffer = 6

// Direction in numpad notation, relative to the player's facing:
//
//	7 8 9
//	4 5 6      6 is forward, 4 is back, 5 is neutral
//	1 2 3
//
// The convention the genre already thinks in, which keeps the motion tables
// below readable as the things they describe.
const (
	DirDownBack = 1 + iota
	DirDown
	DirDownFwd
	DirBack
	DirNeutral
	DirFwd
	DirUpBack
	DirUp
	DirUpFwd
)

// Motion is a recognised directional sequence.
type Motion uint8

const (
	MotionNone Motion = iota
	MotionQCF         // ↓ ↘ →
	MotionQCB         // ↓ ↙ ←
	MotionDP          // → ↓ ↘
)

// The motion table, **in priority order** — the first match wins.
//
// DP must come before QCF and this is not a preference. The DP motion contains
// a QCF-like subsequence, so a backward scan that tried QCF first would match
// the ↓↘→ inside every dragon punch and turn every intended reversal into a
// fireball. It is the classic bug in this system; TestDPBeatsQCF is the whole
// reason the ordering is data and not an accident of if-statement order.
//
// Sequences are chronological — first element pressed first. 13-frame windows
// per the design note; the scan is lenient, so intermediate directions may be
// skipped as long as these land in order inside the window.
//
// Adding HCF for the grappler's command throw, or a charge motion if a charge
// character ever exists, is a row here and nothing else. Neither is built:
// charge is expansion-only by the roster plan's own account, and building an
// input path no character uses is how engines grow dead code.
var motions = []struct {
	motion Motion
	seq    []uint8
	window uint32
}{
	{MotionDP, []uint8{DirFwd, DirDown, DirDownFwd}, 13},
	{MotionQCB, []uint8{DirDown, DirDownBack, DirBack}, 13},
	{MotionQCF, []uint8{DirDown, DirDownFwd, DirFwd}, 13},
}

// direction converts a bitfield to facing-relative numpad.
//
// SOCD is already resolved, so opposing pairs cannot both be set; if one ever
// is, the arithmetic still lands on a real direction rather than a bad index.
func direction(bits uint16, facing int32) uint8 {
	var h, v int32
	if bits&InRight != 0 {
		h += facing // facing right, right is forward
	}
	if bits&InLeft != 0 {
		h -= facing
	}
	if bits&InUp != 0 {
		v++
	}
	if bits&InDown != 0 {
		v--
	}
	// The numpad *is* this formula: 5 is neutral, forward adds 1, up adds 3.
	return uint8(5 + h + 3*v)
}

// at returns the bitfield from frame now-back, or 0 when that frame predates
// the history. Frames never written read as neutral, which matches no motion.
func (p *PlayerState) at(now, back uint32) uint16 {
	if back > now || back >= InputHistory {
		return 0
	}
	return uint16(p.Inputs[(now-back)%InputHistory])
}

// Every query below comes in two forms, and the difference is not cosmetic.
//
// Advance records inputs at step 1 and increments Frame at step 9, so *during*
// a frame the newest ring entry is Frame, and *after* Advance returns it is
// Frame-1. The -At forms take that frame explicitly and are what sim code
// inside Advance calls (step 2 passes s.Frame); the exported forms are for
// callers looking at a finished state, and pass latest().
//
// Guessing which one applies from inside a helper is how a state machine ends
// up reading last frame's input for a whole match.

// latest is the newest recorded frame, from outside Advance.
func (s *GameState) latest() uint32 {
	if s.Frame == 0 {
		return 0
	}
	return s.Frame - 1
}

// Direction is the player's current facing-relative direction.
func (s *GameState) Direction(player int) uint8 {
	return s.DirectionAt(player, s.latest())
}

func (s *GameState) DirectionAt(player int, now uint32) uint8 {
	p := &s.Players[player]
	return direction(p.at(now, 0), p.Facing)
}

// Motion returns the highest-priority motion the player has just completed, or
// MotionNone.
//
// The scan runs **backward** from the newest frame, matching the sequence from
// its last element to its first. That is what produces SF6-style leniency for
// free: intermediate directions, repeats and neutral frames are simply skipped,
// and only the ordering of the endpoints inside the window is required. A
// forward scan would have to enumerate every acceptable variation instead.
func (s *GameState) Motion(player int) Motion {
	return s.MotionAt(player, s.latest())
}

func (s *GameState) MotionAt(player int, now uint32) Motion {
	p := &s.Players[player]
	for _, m := range motions {
		if p.matches(now, m.seq, m.window) {
			return m.motion
		}
	}
	return MotionNone
}

func (p *PlayerState) matches(now uint32, seq []uint8, window uint32) bool {
	k := len(seq) - 1
	for back := uint32(0); back < window && back <= now; back++ {
		if direction(p.at(now, back), p.Facing) == seq[k] {
			k--
			if k < 0 {
				return true
			}
		}
	}
	return false
}

// Pressed reports the press edge: held this frame, not held the frame before.
//
// Without the edge, holding a button re-triggers its move on every frame it is
// down. The bitfield carries held state, never events, so the edge is computed
// here from two frames of history rather than sent over the wire.
func (s *GameState) Pressed(player int, button uint16) bool {
	return s.PressedAt(player, button, s.latest())
}

func (s *GameState) PressedAt(player int, button uint16, now uint32) bool {
	p := &s.Players[player]
	return p.at(now, 0)&button != 0 && p.at(now, 1)&button == 0
}

// pressFrame is the newest frame within the buffer window on which button was
// freshly pressed, or -1.
//
// The frame and not just a yes/no, because the state machine has to know *which*
// press it is spending: moveFor picks the newest live press and enterMove marks
// everything up to that frame spent (PlayerState.Eaten). A bool cannot express
// either half.
//
// int32 rather than uint32: "no press" needs a value outside the frame numbers,
// and every field it is compared against is int32 for the state's layout rule.
func (p *PlayerState) pressFrame(now uint32, button uint16) int32 {
	for back := uint32(0); back < InputBuffer && back <= now; back++ {
		if p.at(now, back)&button != 0 && p.at(now, back+1)&button == 0 {
			return int32(now - back)
		}
	}
	return -1
}

// Buffered reports a press edge within the last InputBuffer frames, which is
// what makes cancels and links feel responsive instead of punishing: the press
// stays live briefly, looking for a state that can act on it.
//
// The query ignores whether the press was already spent — it answers "was this
// pressed recently", which is what a debug overlay and a test want. The state
// machine's own consumption runs through moveFor.
func (s *GameState) Buffered(player int, button uint16) bool {
	return s.BufferedAt(player, button, s.latest())
}

func (s *GameState) BufferedAt(player int, button uint16, now uint32) bool {
	return s.Players[player].pressFrame(now, button) >= 0
}
