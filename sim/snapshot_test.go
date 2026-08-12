package sim

import "testing"

func u32(b []byte) uint32 {
	return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24
}

func i32(b []byte) int32 { return int32(u32(b)) }

// The layout is a contract with the TypeScript reader, which has no way to
// check it. If these offsets move, client/src/sim/wasm.ts moves with them.
func TestSnapshotLayout(t *testing.T) {
	s := New()
	s.Frame = 0x01020304
	s.Hitstop = 7
	s.CamX = FromInt(-60)
	s.Players[0].X = FromInt(-25)
	s.Players[0].Y = FromInt(10)
	s.Players[1].Health = 4321

	b := make([]byte, SnapshotSize)
	s.WriteSnapshot(b)

	if got := u32(b); got != 0x01020304 {
		t.Errorf("frame = %#x", got)
	}
	if got := i32(b[4:]); got != int32(FromInt(-60)) {
		t.Errorf("camX = %d", got)
	}
	if got := i32(b[8:]); got != 7 {
		t.Errorf("hitstop = %d", got)
	}

	p0 := b[12:]
	if got := i32(p0); got != int32(FromInt(-25)) {
		t.Errorf("p0 X = %d", got)
	}
	if got := i32(p0[4:]); got != int32(FromInt(10)) {
		t.Errorf("p0 Y = %d", got)
	}
	if got := i32(p0[8:]); got != 1 {
		t.Errorf("p0 facing = %d", got)
	}
	if got := i32(p0[12:]); got != -1 {
		t.Errorf("p0 move index = %d, want -1 when not attacking", got)
	}

	p1 := b[12+PlayerSnapshotSize:]
	if got := i32(p1[8:]); got != -1 {
		t.Errorf("p1 facing = %d, want -1", got)
	}
	if got := i32(p1[24:]); got != 4321 {
		t.Errorf("p1 health = %d", got)
	}
}

// The overlay must show the boxes the sim actually collides. Reading them from
// anywhere else would let the picture agree with itself and disagree with the
// game.
func TestSnapshotCarriesTheCollidingBoxes(t *testing.T) {
	s := New()
	// Step to the first active frame of the jab.
	s.Advance([2]uint16{InLP, 0})
	for s.Players[0].StateFrame < char().Moves[0].Startup {
		s.Advance([2]uint16{0, 0})
	}

	var want [MaxBoxes]Box
	nHit := s.Hitboxes(0, &want)
	if nHit == 0 {
		t.Fatal("setup: no hitbox on an active frame")
	}

	b := make([]byte, SnapshotSize)
	s.WriteSnapshot(b)

	p0 := b[12:]
	if got := u32(p0[44:]); got == 0 {
		t.Error("no hurtboxes in the snapshot")
	}

	hitOff := 48 + MaxBoxes*boxSize
	if got := u32(p0[hitOff:]); int32(got) != nHit {
		t.Fatalf("hit count = %d, want %d", got, nHit)
	}
	box := p0[hitOff+4:]
	if i32(box) != int32(want[0].X) || i32(box[4:]) != int32(want[0].Y) ||
		i32(box[8:]) != int32(want[0].W) || i32(box[12:]) != int32(want[0].H) {
		t.Errorf("hitbox in the snapshot does not match the one the sim collides: %+v", want[0])
	}

	// The pushbox is always present.
	push := p0[28:]
	if i32(push[8:]) <= 0 || i32(push[12:]) <= 0 {
		t.Error("pushbox has no size")
	}
}

// The buffer is reused every frame, so a frame with fewer boxes than the last
// must not leave the previous frame's values behind for the view to find.
func TestSnapshotLeavesNoStaleBoxes(t *testing.T) {
	b := make([]byte, SnapshotSize)

	// A frame with a live hitbox.
	s := New()
	s.Advance([2]uint16{InLP, 0})
	for s.Players[0].StateFrame < char().Moves[0].Startup {
		s.Advance([2]uint16{0, 0})
	}
	s.WriteSnapshot(b)

	hitOff := 12 + 48 + MaxBoxes*boxSize
	if u32(b[hitOff:]) == 0 {
		t.Fatal("setup: expected a hitbox")
	}

	// ...then an idle frame, into the same buffer.
	idle := New()
	idle.WriteSnapshot(b)

	if got := u32(b[hitOff:]); got != 0 {
		t.Errorf("hit count = %d after an idle frame", got)
	}
	for i, c := range b[hitOff+4 : hitOff+4+MaxBoxes*boxSize] {
		if c != 0 {
			t.Fatalf("hitbox byte %d is %#x: last frame's box is still there", i, c)
		}
	}
}
