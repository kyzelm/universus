package sim

// The render snapshot: everything the view needs to draw a frame, and nothing
// else. GameState itself never crosses the WASM boundary — the view reads this
// and draws it, and it never writes back.
//
// Layout, little-endian, no padding:
//
//	 0  uint32  frame
//	 4  int32   player 0 X   (Fix)
//	 8  int32   player 0 Y   (Fix)
//	12  int32   player 1 X   (Fix)
//	16  int32   player 1 Y   (Fix)
//
// Positions stay in Fix. Converting to units is the view's job, because the
// view is the only place allowed to have fractions that are not exact.
const SnapshotSize = 4 + 2*8

// ponytail: hand-rolled little-endian stores. encoding/binary would do this,
// but sim is a zero-dependency package on purpose and this is four lines.
func putU32(b []byte, v uint32) {
	b[0] = byte(v)
	b[1] = byte(v >> 8)
	b[2] = byte(v >> 16)
	b[3] = byte(v >> 24)
}

// WriteSnapshot packs the render snapshot into b, which must be at least
// SnapshotSize bytes. It allocates nothing: the caller owns the buffer and
// reuses it every frame.
func (s *GameState) WriteSnapshot(b []byte) {
	_ = b[SnapshotSize-1] // bounds check once, up front

	putU32(b, s.Frame)
	for i := range s.Players {
		o := 4 + i*8
		putU32(b[o:], uint32(s.Players[i].X))
		putU32(b[o+4:], uint32(s.Players[i].Y))
	}
}
