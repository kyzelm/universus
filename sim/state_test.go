package sim

import (
	"reflect"
	"testing"
	"unsafe"
)

// The state must stay flat and fixed-size: saveState/loadState are memcpys and
// the checksum is a hash over a byte range. This fails the moment someone adds
// a slice, a map or a pointer — which is exactly when it should fail.
func TestGameStateIsFlatAndFixedSize(t *testing.T) {
	// Comparable at all: a slice/map/func field would make this line not compile.
	if New() == (GameState{}) {
		t.Error("New() returned the zero value; players should start apart")
	}
	if got := unsafe.Sizeof(GameState{}); got > 2048 {
		t.Errorf("sizeof(GameState) = %d, over the 2 KB target", got)
	}
	if got := unsafe.Alignof(GameState{}); got != 4 {
		t.Errorf("alignof(GameState) = %d, want 4 — an 8-byte field forces padding", got)
	}
}

// Checksum hashes the struct's own memory, so a single byte of implicit padding
// is a byte of uninitialised memory in the hash — two machines agree on every
// field and still disagree on the checksum, which presents as a desync with no
// cause visible anywhere in the gameplay code.
//
// Walking the layout by reflection rather than pinning a hand-computed size:
// the point is that *adding a field* cannot introduce padding unnoticed, and a
// magic constant only tells you the size changed, not whether it is sound.
func TestGameStateHasNoImplicitPadding(t *testing.T) {
	assertPacked(t, reflect.TypeOf(GameState{}), "GameState")
}

func assertPacked(t *testing.T, typ reflect.Type, path string) {
	t.Helper()

	switch typ.Kind() {
	case reflect.Struct:
		var end uintptr
		for i := range typ.NumField() {
			f := typ.Field(i)
			if f.Offset != end {
				t.Errorf("%s.%s is at offset %d, want %d: %d padding byte(s) before it",
					path, f.Name, f.Offset, end, f.Offset-end)
			}
			assertPacked(t, f.Type, path+"."+f.Name)
			end += f.Type.Size()
		}
		if end != typ.Size() {
			t.Errorf("%s is %d bytes but its fields total %d: %d trailing padding byte(s)",
				path, typ.Size(), end, typ.Size()-end)
		}

	// Elements of a padding-free type are contiguous, so the element type is
	// the whole question for an array.
	case reflect.Array:
		assertPacked(t, typ.Elem(), path+"[]")

	case reflect.Int32, reflect.Uint32:
		// The width the whole scheme depends on.

	case reflect.Float32, reflect.Float64:
		// CI greps for the type names; this catches one reached through an
		// alias, where the grep would see a harmless-looking identifier.
		t.Errorf("%s is %s — the sim is fixed-point only", path, typ.Kind())

	default:
		t.Errorf("%s is %s: every field must be a 4-byte integer type, "+
			"or the struct picks up padding and Checksum stops being sound", path, typ.Kind())
	}
}

func run(s *GameState, frames int, in [2]uint16) {
	for range frames {
		s.Advance(in)
	}
}

func TestIdleIsStable(t *testing.T) {
	s := New()
	start := s
	run(&s, 600, [2]uint16{})
	for i, p := range s.Players {
		if p != start.Players[i] {
			t.Errorf("player %d drifted while idle: %+v -> %+v", i, start.Players[i], p)
		}
	}
	if s.Frame != 600 {
		t.Errorf("Frame = %d, want 600", s.Frame)
	}
}

func TestGravityNeverSinksBelowGround(t *testing.T) {
	s := New()
	for f := range 300 {
		s.Advance([2]uint16{InUp, InUp}) // hold up: jump, land, jump again
		for i, p := range s.Players {
			if p.Y < GroundY {
				t.Fatalf("frame %d: player %d fell through the floor: Y = %d", f, i, p.Y)
			}
		}
	}
}

func TestJumpRisesAndLands(t *testing.T) {
	s := New()
	s.Advance([2]uint16{InUp, 0})

	airborne, peak := 1, s.Players[0].Y
	for s.Players[0].Y > GroundY {
		s.Advance([2]uint16{}) // input released — the arc is committed
		airborne++
		if s.Players[0].Y > peak {
			peak = s.Players[0].Y
		}
	}

	// ~42 frames at -0.375/frame^2 from 8.0/frame. A constants change that
	// blows past this range is a balance decision, not an accident.
	if airborne < 38 || airborne > 46 {
		t.Errorf("jump lasted %d frames, want 38..46", airborne)
	}
	if peak.ToInt() < 80 {
		t.Errorf("jump peaked at %d units, want >= 80", peak.ToInt())
	}
	if s.Players[0].VY != 0 {
		t.Errorf("VY = %d after landing, want 0", s.Players[0].VY)
	}
}

func TestWallsClamp(t *testing.T) {
	s := New()
	run(&s, 1000, [2]uint16{InLeft, InRight})

	wantLeft := -StageHalfWidth + PlayerHalfWidth
	wantRight := StageHalfWidth - PlayerHalfWidth
	if s.Players[0].X != wantLeft {
		t.Errorf("player 0 X = %d, want %d", s.Players[0].X, wantLeft)
	}
	if s.Players[1].X != wantRight {
		t.Errorf("player 1 X = %d, want %d", s.Players[1].X, wantRight)
	}
}

func TestPlayersDoNotMerge(t *testing.T) {
	s := New()
	for f := range 500 {
		s.Advance([2]uint16{InRight, InLeft}) // walk into each other and keep pushing
		gap := (s.Players[1].X - s.Players[0].X).Abs()
		if gap < PlayerHalfWidth*2-One {
			t.Fatalf("frame %d: pushboxes merged, gap = %d units", f, gap.ToInt())
		}
	}
}

func TestJumpingOverAnotherPlayerIgnoresPushbox(t *testing.T) {
	s := New()
	s.Players[0].X = 0
	s.Players[1].X = 0
	s.Players[1].Y = PlayerHeight + One // clear overhead
	before := s.Players[0].X

	s.Advance([2]uint16{})
	if s.Players[0].X != before {
		t.Errorf("player 0 was pushed by a player stacked above it: %d -> %d", before, s.Players[0].X)
	}
}

// The whole architecture rests on this: same start, same inputs, same bytes.
func TestSameInputsProduceIdenticalState(t *testing.T) {
	inputs := inputSeq(2000)

	a, b := New(), New()
	for _, in := range inputs {
		a.Advance(in)
	}
	for _, in := range inputs {
		b.Advance(in)
	}
	if a != b {
		t.Fatalf("identical inputs diverged:\n a = %+v\n b = %+v", a, b)
	}
}
