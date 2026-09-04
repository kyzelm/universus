package sim

// The render snapshot: everything the view needs to draw a frame, and nothing
// else. GameState itself never crosses the WASM boundary — the view reads this
// and draws it, and it never writes back.
//
// Layout, little-endian, no padding. All coordinates stay in Fix; converting to
// units is the view's job, because the view is the only place allowed to have
// fractions that are not exact.
//
//	 0  uint32  frame
//	 4  int32   camera centre X (Fix)
//	 8  int32   hitstop frames remaining
//	12  player 0 block
//	12+PlayerSnapshotSize    player 1 block
//	12+2*PlayerSnapshotSize  uint32 projectile count, then MaxProjectiles boxes
//
// Player block:
//
//	 0  int32   X (Fix)          20  int32  state frame
//	 4  int32   Y (Fix)          24  int32  health
//	 8  int32   facing           28  int32  drive
//	12  int32   move index       32  int32  super
//	16  int32   state            36  int32  burnout
//	40  int32   combo hits       44  int32  counter class
//	48  pushbox (4 x int32)
//	64  uint32  hurt count       68  hurtboxes (MaxBoxes x 4 x int32)
//	68+64 = 132  uint32 hit count
//	136 hitboxes (MaxBoxes x 4 x int32)
//
// Boxes ride along for the debug overlay. It is the tool that debugs every
// system built on top of them, so it is built early and it reads the same boxes
// the sim collides — an overlay drawn from separate numbers would confirm the
// wrong thing.
const (
	boxSize            = 4 * 4
	PlayerSnapshotSize = 12*4 + boxSize + 2*(4+MaxBoxes*boxSize)
	projOffset         = 3*4 + 2*PlayerSnapshotSize
	SnapshotSize       = projOffset + 4 + MaxProjectiles*boxSize
)

// ponytail: hand-rolled little-endian stores. encoding/binary would do this,
// but sim is a zero-dependency package on purpose and this is four lines.
func putU32(b []byte, v uint32) {
	b[0] = byte(v)
	b[1] = byte(v >> 8)
	b[2] = byte(v >> 16)
	b[3] = byte(v >> 24)
}

func putI32(b []byte, v int32) { putU32(b, uint32(v)) }

func putBox(b []byte, x Box) {
	putI32(b, int32(x.X))
	putI32(b[4:], int32(x.Y))
	putI32(b[8:], int32(x.W))
	putI32(b[12:], int32(x.H))
}

// WriteSnapshot packs the render snapshot into b, which must be at least
// SnapshotSize bytes. It allocates nothing: the caller owns the buffer and
// reuses it every frame.
func (s *GameState) WriteSnapshot(b []byte) {
	_ = b[SnapshotSize-1] // bounds check once, up front

	// Unused box slots are not written below, so without this they keep last
	// frame's values. The view reads the counts and would never look at them —
	// but a buffer where some bytes are current and some are stale is a trap
	// for the next person, and a ~400-byte clear costs nothing.
	clear(b[:SnapshotSize])

	putU32(b, s.Frame)
	putI32(b[4:], int32(s.CamX))
	putI32(b[8:], s.Hitstop)

	var boxes [MaxBoxes]Box
	for i := range s.Players {
		p := &s.Players[i]
		o := b[12+i*PlayerSnapshotSize:]

		putI32(o, int32(p.X))
		putI32(o[4:], int32(p.Y))
		putI32(o[8:], p.Facing)
		putI32(o[12:], p.MoveIndex)
		putI32(o[16:], p.State)
		putI32(o[20:], p.StateFrame)
		putI32(o[24:], p.Health)
		// The gauges ride along with health: they are the two most-read HUD
		// elements after it, and Burnout has to be unmistakable on screen.
		putI32(o[28:], p.Drive)
		putI32(o[32:], p.Super)
		putI32(o[36:], p.Burnout)
		// The combo counter and the counter-hit class. A counter hit that is
		// not visible is one nobody learns from, which is the only reason
		// either of them crosses.
		putI32(o[40:], p.Combo)
		putI32(o[44:], p.Counter)
		putBox(o[48:], s.Pushbox(i))

		n := s.Hurtboxes(i, &boxes)
		putU32(o[64:], uint32(n))
		for k := int32(0); k < n; k++ {
			putBox(o[68+int(k)*boxSize:], boxes[k])
		}

		hitOff := 68 + MaxBoxes*boxSize
		n = s.Hitboxes(i, &boxes)
		putU32(o[hitOff:], uint32(n))
		for k := int32(0); k < n; k++ {
			putBox(o[hitOff+4+int(k)*boxSize:], boxes[k])
		}
	}

	// Projectiles are packed from the front, not by slot: the view draws them,
	// it never has to name one, and a count plus that many boxes is the least
	// the reader has to know.
	live := 0
	for i := range s.Projectiles {
		pb := s.ProjectileBox(i)
		if pb.Empty() {
			continue
		}
		putBox(b[projOffset+4+live*boxSize:], pb)
		live++
	}
	putU32(b[projOffset:], uint32(live))
}
