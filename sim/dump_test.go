package sim

import (
	"strings"
	"testing"
)

// The dump is only worth having if it names the field that differs. A checksum
// already says two states are not equal; this is the part that says how.
func TestDumpNamesTheDifferingField(t *testing.T) {
	a := New()
	b := New()
	b.Players[1].Health -= 7

	da, db := strings.Split(a.Dump(), "\n"), strings.Split(b.Dump(), "\n")
	if len(da) != len(db) {
		t.Fatalf("dumps have different line counts: %d vs %d", len(da), len(db))
	}

	var differing []string
	for i := range da {
		if da[i] != db[i] {
			differing = append(differing, da[i]+" | "+db[i])
		}
	}
	if len(differing) != 1 {
		t.Fatalf("want exactly one differing line, got %d: %v", len(differing), differing)
	}
	if !strings.Contains(differing[0], "Players[1].Health") {
		t.Errorf("the differing line does not name the field: %s", differing[0])
	}
}

// Same state, same text — or the gate would report a divergence between two
// identical states, which is worse than reporting none.
func TestDumpIsStable(t *testing.T) {
	s := New()
	for range 50 {
		s.Advance([2]uint16{InRight | InLP, InLeft})
	}
	if s.Dump() != s.Dump() {
		t.Error("Dump is not stable across calls")
	}
}

// Every field must appear, including ones added later. The field nobody
// remembered to print is the one causing the desync.
func TestDumpCoversEveryField(t *testing.T) {
	start := New()
	out := start.Dump()
	for _, want := range []string{
		"Frame", "RNG", "Hitstop", "CamX",
		"Players[0].X", "Players[0].Y", "Players[0].VX", "Players[0].VY",
		"Players[0].Facing", "Players[0].Char", "Players[0].Health",
		"Players[0].State", "Players[0].StateFrame", "Players[0].MoveIndex",
		"Players[0].HasHit", "Players[0].Stun", "Players[0].JumpVX",
		"Players[0].Inputs", "Players[1].X",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("%s is missing from the dump", want)
		}
	}
}

// The input history is 32 entries per player and mostly zero. Sixty lines of
// "0" per dump buries the two that matter, so runs collapse — but a differing
// run length still has to show up as a difference.
func TestDumpCollapsesZeroRunsWithoutHidingThem(t *testing.T) {
	a := New()
	if !strings.Contains(a.Dump(), "Inputs[0:32] = zero") {
		t.Error("a fresh state should collapse the whole input ring")
	}

	b := New()
	b.Players[0].Inputs[5] = uint32(InLP)
	out := b.Dump()
	if !strings.Contains(out, "Players[0].Inputs[5] = 16") {
		t.Error("a non-zero entry must be printed individually")
	}
	if out == a.Dump() {
		t.Error("collapsing hid a real difference")
	}
}
