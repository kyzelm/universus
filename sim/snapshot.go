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
//	12+PlayerSnapshotSize  player 1 block
//
// Player block:
//
//	 0  int32   X (Fix)          16  int32  state
//	 4  int32   Y (Fix)          20  int32  state frame
//	 8  int32   facing           24  int32  health
//	12  int32   move index       28  pushbox   (4 x int32)
//	44  uint32  hurt count       48  hurtboxes (MaxBoxes x 4 x int32)
//	48+64 = 112  uint32 hit count
//	116 hitboxes (MaxBoxes x 4 x int32)
//
// Boxes ride along for the debug overlay. It is the tool that debugs every
// system built on top of them, so it is built early and it reads the same boxes
// the sim collides — an overlay drawn from separate numbers would confirm the
// wrong thing.
const (
	boxSize            = 4 * 4
	PlayerSnapshotSize = 7*4 + boxSize + 2*(4+MaxBoxes*boxSize)
	SnapshotSize       = 3*4 + 2*PlayerSnapshotSize
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
		putBox(o[28:], s.Pushbox(i))

		n := s.Hurtboxes(i, &boxes)
		putU32(o[44:], uint32(n))
		for k := int32(0); k < n; k++ {
			putBox(o[48+int(k)*boxSize:], boxes[k])
		}

		hitOff := 48 + MaxBoxes*boxSize
		n = s.Hitboxes(i, &boxes)
		putU32(o[hitOff:], uint32(n))
		for k := int32(0); k < n; k++ {
			putBox(o[hitOff+4+int(k)*boxSize:], boxes[k])
		}
	}
}
