package sim

import "testing"

// A fixed, varied input pattern — no clock, no rand, reproducible anywhere.
//
// Attack buttons are in here as well as directions. A directions-only sequence
// never enters an attack, a hitstun or a hitstop, so it would exercise the
// rollback machinery over about a third of the state and report success.
func inputSeq(n int) [][2]uint16 {
	buttons := [...]uint16{InLP, InMP, InHP, InLK, InMK, InHK}
	in := make([][2]uint16, n)

	for f := range in {
		p0 := uint16(f*7%13) & 0xF
		p1 := uint16(f*11%17) & 0xF
		// Often enough to collide and be interrupted, rarely enough that the
		// players are not permanently mid-move.
		if f%5 == 0 {
			p0 |= buttons[f/5%len(buttons)]
		}
		if f%7 == 0 {
			p1 |= buttons[f/7%len(buttons)]
		}
		in[f] = [2]uint16{p0, p1}
	}
	return in
}

// The rollback correctness test. Run 100 frames straight; run the same 100
// frames again while forcing rollbacks of depth 1..8 along the way. The final
// checksums must be identical.
//
// This fails immediately if any gameplay state ever lives outside GameState,
// which is the only reason it is worth having.
func TestRollbackReplayMatchesStraightRun(t *testing.T) {
	const frames = 100
	in := inputSeq(frames)

	ref := NewSession()
	for _, i := range in {
		ref.Advance(i)
	}
	want := ref.Checksum()

	s := NewSession()
	for f, i := range in {
		s.Advance(i)
		if f < MaxRollback || f%4 != 0 {
			continue
		}
		depth := uint32(f%MaxRollback) + 1
		target := s.Frame() - depth
		if !s.Adjust(target, in[target]) {
			t.Fatalf("frame %d: rollback of depth %d rejected", f, depth)
		}
	}

	if s.Frame() != frames {
		t.Errorf("Frame = %d after replay, want %d", s.Frame(), frames)
	}
	if got := s.Checksum(); got != want {
		t.Errorf("checksum after rollbacks = %#x, want %#x", got, want)
	}
}

// A rollback that replays the same inputs is a no-op by design, so prove the
// corrected input is actually the one replayed.
func TestAdjustReplaysTheCorrectedInput(t *testing.T) {
	s := NewSession()
	for range 20 {
		s.Advance([2]uint16{})
	}
	idle := s.State().Players[0].X

	if !s.Adjust(15, [2]uint16{InRight, 0}) {
		t.Fatal("in-window rollback rejected")
	}
	if s.Frame() != 20 {
		t.Errorf("Frame = %d after replay, want 20", s.Frame())
	}
	if s.State().Players[0].X == idle {
		t.Error("player 0 did not move; the replay ignored the corrected input")
	}
}

func TestAdjustRejectsOutsideTheWindow(t *testing.T) {
	s := NewSession()
	for range 40 {
		s.Advance([2]uint16{})
	}
	now := s.Frame()

	if s.Adjust(now, [2]uint16{}) {
		t.Error("the current frame has not been advanced yet; it cannot be rolled back")
	}
	if s.Adjust(now-MaxRollback-1, [2]uint16{}) {
		t.Error("a frame past MaxRollback was accepted; that snapshot may be gone")
	}
	if !s.Adjust(now-MaxRollback, [2]uint16{}) {
		t.Error("the oldest frame in the window was rejected")
	}
}

func BenchmarkAdvance(b *testing.B) {
	s, in := NewSession(), inputSeq(64)
	for i := 0; b.Loop(); i++ {
		s.Advance(in[i%64])
	}
}

// One frame plus a full-depth correction — 9 sim steps, the worst case that
// still has to fit in 16.6 ms. Target: < 4 ms in WASM.
func BenchmarkAdvanceWithRollback8(b *testing.B) {
	s, in := NewSession(), inputSeq(64)
	for i := range MaxRollback * 2 {
		s.Advance(in[i%64])
	}
	for i := 0; b.Loop(); i++ {
		s.Advance(in[i%64])
		f := s.Frame() - MaxRollback
		s.Adjust(f, in[int(f)%64])
	}
}
