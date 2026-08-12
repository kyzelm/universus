package sim

import "testing"

// Numpad direction to bitfield, for a player facing right. Tests read as the
// motions they describe rather than as bit arithmetic.
var pad = map[uint8]uint16{
	1: InDown | InLeft, 2: InDown, 3: InDown | InRight,
	4: InLeft, 5: 0, 6: InRight,
	7: InUp | InLeft, 8: InUp, 9: InUp | InRight,
}

// feed advances the state one frame per direction, giving player 0 the input
// and player 1 neutral. Buttons may be or-ed into the final frame.
func feed(s *GameState, dirs ...uint8) {
	for _, d := range dirs {
		s.Advance([2]uint16{pad[d], 0})
	}
}

func TestDirectionIsNumpad(t *testing.T) {
	for want, bits := range pad {
		if got := direction(bits, 1); got != want {
			t.Errorf("facing right, bits %04b: direction = %d, want %d", bits, got, want)
		}
	}
}

// The same physical input is a different numpad direction depending on which
// way the player faces — that is the entire point of facing-relative notation,
// and getting it wrong means P2 cannot throw a fireball.
func TestDirectionMirrorsWithFacing(t *testing.T) {
	mirror := map[uint8]uint8{1: 3, 2: 2, 3: 1, 4: 6, 5: 5, 6: 4, 7: 9, 8: 8, 9: 7}
	for d, want := range mirror {
		if got := direction(pad[d], -1); got != want {
			t.Errorf("facing left, right-facing %d: direction = %d, want %d", d, got, want)
		}
	}
}

func TestMotionsAreRecognised(t *testing.T) {
	for _, tc := range []struct {
		name string
		dirs []uint8
		want Motion
	}{
		{"QCF", []uint8{2, 3, 6}, MotionQCF},
		{"QCB", []uint8{2, 1, 4}, MotionQCB},
		{"DP", []uint8{6, 2, 3}, MotionDP},
		{"nothing", []uint8{5, 5, 5}, MotionNone},
		{"walking forward", []uint8{6, 6, 6, 6}, MotionNone},
		{"crouching", []uint8{2, 2, 2, 2}, MotionNone},
		{"wrong order", []uint8{6, 3, 2}, MotionNone},

		// ↓ → with no ↘ at all. The design note specifies QCF as three
		// directions, and leniency means skipping *junk between* them, not
		// dropping one of them — so this is a miss, deliberately.
		//
		// Worth revisiting for keyboard specifically: a diagonal costs two
		// simultaneous keys, and dropping it is a far more common miss on
		// keyboard than on a stick or a leverless. Loosening it is a game
		// design decision and a Decision Log entry, not a quiet change here.
		{"quarter circle with the diagonal dropped", []uint8{2, 6}, MotionNone},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := New()
			feed(&s, tc.dirs...)
			if got := s.Motion(0); got != tc.want {
				t.Errorf("Motion after %v = %d, want %d", tc.dirs, got, tc.want)
			}
		})
	}
}

// The classic bug in this system, and the reason the motion table is ordered
// data rather than a chain of ifs. A DP is → ↓ ↘, which *contains* ↓ ↘ — the
// quarter-circle forward. Match QCF first and every intended dragon punch comes
// out as a fireball, which is the single most reported problem in amateur
// fighting game engines.
func TestDPBeatsQCF(t *testing.T) {
	s := New()
	feed(&s, 6, 2, 3)
	if got := s.Motion(0); got != MotionDP {
		t.Fatalf("→↓↘ recognised as %d, want DP (%d)", got, MotionDP)
	}

	// The input where priority actually decides. A bare →↓↘ does not contain a
	// QCF under a strict backward scan — QCF needs → *last* and here it comes
	// first — so the case above proves less than it looks.
	//
	// The overlap is real the moment the player ends on forward, which is how
	// a dragon punch is usually thrown: you finish holding toward the opponent.
	// →↓↘→ satisfies both patterns, and only the table order picks the winner.
	s = New()
	feed(&s, 6, 2, 3, 6)

	p := &s.Players[0]
	if !p.matches(s.latest(), seqOf(t, MotionQCF), 13) {
		t.Fatal("→↓↘→ does not match QCF; this test has stopped testing priority")
	}
	if !p.matches(s.latest(), seqOf(t, MotionDP), 13) {
		t.Fatal("→↓↘→ does not match DP")
	}
	if got := s.Motion(0); got != MotionDP {
		t.Errorf("→↓↘→ resolved to %d, want DP (%d) — QCF is winning the scan, "+
			"which is every intended reversal coming out as a fireball", got, MotionDP)
	}
}

// By value, not by index: the point is which motion wins, not where it sits.
func seqOf(t *testing.T, m Motion) []uint8 {
	t.Helper()
	for _, e := range motions {
		if e.motion == m {
			return e.seq
		}
	}
	t.Fatalf("motion %d is not in the table", m)
	return nil
}

// Backward scanning buys leniency for free: a real player rolls through extra
// directions and holds each for several frames, and none of that should matter
// as long as the endpoints land in order inside the window.
func TestRecognitionIsLenient(t *testing.T) {
	for _, tc := range []struct {
		name string
		dirs []uint8
	}{
		{"held directions", []uint8{2, 2, 2, 3, 3, 6, 6}},
		{"rolled through down-back first", []uint8{1, 2, 3, 6}},
		{"neutral frame mid-motion", []uint8{2, 5, 3, 6}},
		{"preceded by junk", []uint8{8, 4, 5, 2, 3, 6}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := New()
			feed(&s, tc.dirs...)
			if got := s.Motion(0); got != MotionQCF {
				t.Errorf("%v recognised as %d, want QCF", tc.dirs, got)
			}
		})
	}
}

func TestMotionExpiresWithItsWindow(t *testing.T) {
	s := New()
	feed(&s, 2, 3, 6)
	if s.Motion(0) != MotionQCF {
		t.Fatal("QCF not recognised on the frame it completed")
	}

	// The window is 13 frames counting back from now, so the ↓ that started it
	// falls out after enough neutral frames and the motion stops matching.
	for f := range 20 {
		feed(&s, 5)
		if s.Motion(0) == MotionNone {
			if f < 9 {
				t.Fatalf("QCF expired after only %d neutral frames, window is 13", f+1)
			}
			return
		}
	}
	t.Error("QCF still matching 20 frames after it completed — the window is not bounded")
}

// P2 starts facing left, so a fireball is the mirrored physical input. If this
// fails, half the roster cannot use its specials.
func TestMotionIsFacingRelative(t *testing.T) {
	s := New()
	for _, d := range []uint8{2, 1, 4} { // ↓ ↙ ← in absolute terms
		s.Advance([2]uint16{0, pad[d]})
	}

	if got := s.Motion(1); got != MotionQCF {
		t.Errorf("↓↙← for a left-facing player = %d, want QCF (%d)", got, MotionQCF)
	}
	if got := s.Motion(0); got != MotionNone {
		t.Errorf("player 0 got %d from player 1's inputs", got)
	}
}

// A history ring that is not in GameState reads as a rollback that replays a
// fireball as a crouch. This is the test that would catch it.
func TestInputHistoryRollsBack(t *testing.T) {
	s := New()
	feed(&s, 2, 3)

	saved := s
	feed(&s, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5)
	if s.Motion(0) != MotionNone {
		t.Fatal("setup: the partial motion should have aged out")
	}

	s = saved
	feed(&s, 6)
	if got := s.Motion(0); got != MotionQCF {
		t.Errorf("after rewind the ↓↘ was gone: Motion = %d, want QCF", got)
	}
}

// The ring is InputHistory long; a motion cannot be allowed to match against
// input from a lap earlier.
func TestHistoryDoesNotWrapAround(t *testing.T) {
	s := New()
	feed(&s, 2, 3, 6)
	for range InputHistory {
		feed(&s, 5)
	}
	if got := s.Motion(0); got != MotionNone {
		t.Errorf("a full lap later the stale QCF still matches: %d", got)
	}
}

func TestPressedIsAnEdge(t *testing.T) {
	s := New()

	s.Advance([2]uint16{InLP, 0})
	if !s.Pressed(0, InLP) {
		t.Error("first frame of a press did not register")
	}

	s.Advance([2]uint16{InLP, 0}) // still held
	if s.Pressed(0, InLP) {
		t.Error("a held button re-triggered; every move would fire every frame")
	}

	s.Advance([2]uint16{0, 0})
	s.Advance([2]uint16{InLP, 0}) // released and pressed again
	if !s.Pressed(0, InLP) {
		t.Error("a second press after a release did not register")
	}
	if s.Pressed(0, InHP) {
		t.Error("a different button reported pressed")
	}
}

func TestBufferedHoldsThePressBriefly(t *testing.T) {
	s := New()
	s.Advance([2]uint16{InLP, 0})

	for f := range InputBuffer {
		if !s.Buffered(0, InLP) {
			t.Fatalf("press went stale after %d frames, buffer is %d", f, InputBuffer)
		}
		s.Advance([2]uint16{0, 0})
	}
	if s.Buffered(0, InLP) {
		t.Errorf("press still buffered past %d frames", InputBuffer)
	}
}

// Everything above is a query over state, so identical inputs must produce
// identical answers — including through the ring's wrap point.
func TestInputQueriesAreDeterministic(t *testing.T) {
	inputs := inputSeq(500)

	a, b := New(), New()
	for _, in := range inputs {
		a.Advance(in)
		b.Advance(in)
		for p := range 2 {
			if a.Motion(p) != b.Motion(p) || a.Direction(p) != b.Direction(p) {
				t.Fatalf("frame %d player %d: queries diverged on identical input", a.Frame, p)
			}
		}
	}
	if a != b {
		t.Error("states diverged")
	}
}
