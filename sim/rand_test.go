package sim

import "testing"

// The property the architecture needs: the sequence is a function of the state,
// so two machines at the same state draw the same numbers.
func TestSameSeedSameSequence(t *testing.T) {
	a, b := New(), New()
	for i := range 1000 {
		x, y := a.Rand(), b.Rand()
		if x != y {
			t.Fatalf("draw %d diverged: %d != %d", i, x, y)
		}
	}
	if a.RNG != b.RNG {
		t.Errorf("generator state diverged: %d != %d", a.RNG, b.RNG)
	}
}

// The reason the generator lives in GameState and not beside it. Save, draw,
// restore, draw again: the second run must repeat the first exactly, or a
// rollback would silently change every random outcome after it.
func TestRandRollsBackWithTheState(t *testing.T) {
	s := New()
	for range 20 {
		s.Rand()
	}

	saved := s // the rollback: one struct assignment, exactly as Session does it

	var want [10]uint32
	for i := range want {
		want[i] = s.Rand()
	}

	s = saved
	for i := range want {
		if got := s.Rand(); got != want[i] {
			t.Fatalf("draw %d after rewind = %d, want %d", i, got, want[i])
		}
	}
	if s != saved && s.RNG == saved.RNG {
		t.Error("replay left the generator where it started")
	}
}

func TestRandAdvancesAndNeverSticks(t *testing.T) {
	s := New()
	prev := s.Rand()
	for i := range 10000 {
		got := s.Rand()
		if got == prev {
			t.Fatalf("draw %d repeated the previous value %d", i, got)
		}
		if got == 0 {
			t.Fatalf("draw %d was zero — xorshift cannot recover from it", i)
		}
		prev = got
	}
}

// A hand-built GameState{} has a zero generator, which is xorshift's fixed
// point. Rand guards it; without the guard this returns zero forever and every
// random decision in a test-built state silently collapses to the same branch.
func TestZeroStateStillGenerates(t *testing.T) {
	var s GameState
	if got := s.Rand(); got == 0 {
		t.Fatal("zero-state Rand() = 0, the generator is dead")
	}
	if s.RNG == 0 {
		t.Error("zero-state Rand() left RNG at 0")
	}
}

func TestRandNStaysInRange(t *testing.T) {
	s := New()
	for _, n := range []uint32{1, 2, 3, 6, 7, 100, 1 << 16} {
		for range 5000 {
			if got := s.RandN(n); got >= n {
				t.Fatalf("RandN(%d) = %d, out of range", n, got)
			}
		}
	}
	if got := s.RandN(0); got != 0 {
		t.Errorf("RandN(0) = %d, want 0 (and no division by zero)", got)
	}
}

// Multiply-shift is easy to get wrong in a way that stays in range — a shift of
// 31 or 33 biases hard toward one end while every value still looks legal. Every
// bucket getting hit is the cheapest check that catches it.
func TestRandNCoversItsRange(t *testing.T) {
	s := New()
	var seen [6]int
	for range 6000 {
		seen[s.RandN(6)]++
	}
	for v, n := range seen {
		if n == 0 {
			t.Errorf("RandN(6) never returned %d in 6000 draws", v)
		}
	}
}
