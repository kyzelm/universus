package sim

import "testing"

// Published FNV-1a 32-bit vectors. If these drift, the hash is not FNV-1a any
// more and every recorded checksum in testdata/ is worthless.
func TestFNV1aMatchesKnownVectors(t *testing.T) {
	for _, c := range []struct {
		in   string
		want uint32
	}{
		{"", 2166136261},
		{"a", 0xe40c292c},
		{"foobar", 0xbf9cf968},
	} {
		if got := fnv1a([]byte(c.in)); got != c.want {
			t.Errorf("fnv1a(%q) = %#x, want %#x", c.in, got, c.want)
		}
	}
}

func TestChecksumTracksState(t *testing.T) {
	a, b := New(), New()
	if a.Checksum() != b.Checksum() {
		t.Fatal("identical states hashed differently")
	}

	b.Players[1].VY = 1 // one bit, in the last field of the struct
	if a.Checksum() == b.Checksum() {
		t.Error("a changed state hashed the same; is the whole struct covered?")
	}
}

// Rollback in miniature: save, run ahead, restore, replay the same inputs.
// Landing on a different checksum means state is living outside GameState.
func TestSaveLoadReplayReproducesChecksum(t *testing.T) {
	inputs := inputSeq(60)

	s := New()
	for _, in := range inputs[:20] {
		s.Advance(in)
	}
	saved := s // the memcpy

	for _, in := range inputs[20:] {
		s.Advance(in)
	}
	want := s.Checksum()

	s = saved // the rewind
	for _, in := range inputs[20:] {
		s.Advance(in)
	}
	if got := s.Checksum(); got != want {
		t.Errorf("replay after rollback gave %#x, want %#x", got, want)
	}
}
