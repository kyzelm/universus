package sim

import (
	"bytes"
	"testing"
)

// The byte layout is a contract with the TypeScript DataView reader. If this
// test changes, client/src/utils/wasm.ts changes in the same commit.
func TestSnapshotLayout(t *testing.T) {
	s := GameState{Frame: 0x01020304}
	s.Players[0] = PlayerState{X: FromInt(-60), Y: FromInt(7)}
	s.Players[1] = PlayerState{X: FromInt(60), Y: 0}

	b := make([]byte, SnapshotSize)
	s.WriteSnapshot(b)

	want := []byte{
		0x04, 0x03, 0x02, 0x01, // frame, little-endian
		0x00, 0x00, 0xC4, 0xFF, // -60 << 16
		0x00, 0x00, 0x07, 0x00, // 7 << 16
		0x00, 0x00, 0x3C, 0x00, // 60 << 16
		0x00, 0x00, 0x00, 0x00, // 0
	}
	if !bytes.Equal(b, want) {
		t.Errorf("snapshot bytes:\n got %v\nwant %v", b, want)
	}
}

func TestWriteSnapshotOverwritesEveryByte(t *testing.T) {
	// A stale byte left behind by a shorter write would desync the view from
	// the sim in a way that only shows up as a flicker. Fill with junk first.
	b := bytes.Repeat([]byte{0xAA}, SnapshotSize)
	(&GameState{}).WriteSnapshot(b)

	if !bytes.Equal(b, make([]byte, SnapshotSize)) {
		t.Errorf("zero state did not zero the buffer: %v", b)
	}
}

func TestWriteSnapshotRejectsShortBuffer(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("WriteSnapshot into a short buffer did not panic")
		}
	}()
	(&GameState{}).WriteSnapshot(make([]byte, SnapshotSize-1))
}

func TestSnapshotTracksAdvance(t *testing.T) {
	s := New()
	b := make([]byte, SnapshotSize)

	s.WriteSnapshot(b)
	first := append([]byte(nil), b...)

	run(&s, 30, [2]uint16{InRight, InLeft})
	s.WriteSnapshot(b)

	if bytes.Equal(b, first) {
		t.Error("snapshot unchanged after 30 frames of walking")
	}
	if b[0] != 30 {
		t.Errorf("snapshot frame = %d, want 30", b[0])
	}
}
