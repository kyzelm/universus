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
	// Position, not the whole struct: StateFrame counting up while idle is the
	// state clock working, not drift.
	for i, p := range s.Players {
		q := start.Players[i]
		if p.X != q.X || p.Y != q.Y || p.VX != 0 || p.VY != 0 || p.Health != q.Health {
			t.Errorf("player %d drifted while idle: %+v -> %+v", i, q, p)
		}
		if p.State != StateIdle {
			t.Errorf("player %d is in state %d after 600 neutral frames", i, p.State)
		}
	}
	if s.Frame != 600 {
		t.Errorf("Frame = %d, want 600", s.Frame)
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

// hold advances n frames with the same input for player 0 and neutral for 1.
func hold(s *GameState, n int, in uint16) {
	for range n {
		s.Advance([2]uint16{in, 0})
	}
}

func TestWalkUsesCharacterSpeed(t *testing.T) {
	s := New()
	start := s.Players[0].X

	hold(&s, 10, InRight) // player 0 faces right, so this is forward
	if got, want := s.Players[0].X-start, char().WalkForward*10; got != want {
		t.Errorf("walked %d in 10 frames, want %d", got, want)
	}

	s = New()
	start = s.Players[0].X
	hold(&s, 10, InLeft)
	if got, want := start-s.Players[0].X, char().WalkBack*10; got != want {
		t.Errorf("walked back %d in 10 frames, want %d", got, want)
	}
}

// Pre-jump frames are grounded. This is what makes throws beat jump attempts,
// so it is a rule and not an animation detail.
func TestPreJumpIsGrounded(t *testing.T) {
	s := New()
	c := char()

	for f := int32(0); f < c.PreJumpFrames; f++ {
		s.Advance([2]uint16{InUp, 0})
		if s.Players[0].State != StatePreJump {
			t.Fatalf("frame %d: state = %d, want pre-jump", f, s.Players[0].State)
		}
		if s.Players[0].Y != GroundY {
			t.Fatalf("frame %d: left the ground during pre-jump", f)
		}
	}
	s.Advance([2]uint16{0, 0})
	if s.Players[0].State != StateAir {
		t.Errorf("state after pre-jump = %d, want air", s.Players[0].State)
	}
}

func TestJumpRisesAndLands(t *testing.T) {
	s := New()
	hold(&s, int(char().PreJumpFrames), InUp)

	airborne, peak := 0, Fix(0)
	for range 200 {
		s.Advance([2]uint16{}) // released: the arc is committed
		if !Airborne(s.Players[0].State) {
			break
		}
		airborne++
		if s.Players[0].Y > peak {
			peak = s.Players[0].Y
		}
	}

	// Derived from the constants rather than pinned: a jump lasts about
	// 2*v/|g| frames, and asserting the relationship survives tuning.
	want := 2 * int(char().JumpVelocity/(-char().Gravity))
	if airborne < want-3 || airborne > want+3 {
		t.Errorf("jump lasted %d frames, want ~%d", airborne, want)
	}
	if peak.ToInt() < 60 {
		t.Errorf("jump peaked at %d units, want >= 60", peak.ToInt())
	}
	if s.Players[0].Y != GroundY || s.Players[0].VY != 0 {
		t.Errorf("landed at Y=%d VY=%d, want 0 and 0", s.Players[0].Y, s.Players[0].VY)
	}
	if s.Players[0].State != StateIdle {
		t.Errorf("state after landing = %d, want idle", s.Players[0].State)
	}
}

// No air control: the arc is committed at pre-jump and cannot be steered.
func TestJumpHasNoAirControl(t *testing.T) {
	s := New()
	hold(&s, int(char().PreJumpFrames)+1, InUp) // neutral jump, now airborne

	x := s.Players[0].X
	for range 10 {
		s.Advance([2]uint16{InRight, 0}) // push forward mid-air
	}
	if s.Players[0].X != x {
		t.Errorf("a neutral jump drifted %d units under forward input", s.Players[0].X-x)
	}
}

func TestGravityNeverSinksBelowGround(t *testing.T) {
	s := New()
	for f := range 300 {
		s.Advance([2]uint16{InUp, InUp})
		for i, p := range s.Players {
			if p.Y < GroundY {
				t.Fatalf("frame %d: player %d fell through the floor: Y = %d", f, i, p.Y)
			}
		}
	}
}

func TestDashCoversItsDistanceAndCommits(t *testing.T) {
	s := New()
	c := char()

	// Two taps of forward with a gap: tap, release, tap.
	s.Advance([2]uint16{InRight, 0})
	s.Advance([2]uint16{0, 0})
	start := s.Players[0].X
	s.Advance([2]uint16{InRight, 0})

	if s.Players[0].State != StateDash {
		t.Fatalf("a double tap forward did not dash: state = %d", s.Players[0].State)
	}

	// Committed: input during the dash cannot change it, including the
	// opposite direction.
	for f := int32(1); f < c.DashFrames; f++ {
		s.Advance([2]uint16{InLeft, 0})
		if s.Players[0].State != StateDash {
			t.Fatalf("frame %d: dash was cancelled by input, state = %d", f, s.Players[0].State)
		}
	}

	s.Advance([2]uint16{0, 0})
	if s.Players[0].State != StateIdle {
		t.Errorf("state after the dash = %d, want idle", s.Players[0].State)
	}

	// Distance is per-frame velocity times frames, so integer division of the
	// distance may leave a remainder — within a unit is the bar.
	moved := s.Players[0].X - start
	if (moved - c.DashDistance).Abs() > One {
		t.Errorf("dash covered %d, want %d", moved, c.DashDistance)
	}
}

func TestBackdashGoesBackwards(t *testing.T) {
	s := New()
	s.Advance([2]uint16{InLeft, 0})
	s.Advance([2]uint16{0, 0})
	start := s.Players[0].X
	s.Advance([2]uint16{InLeft, 0})

	if s.Players[0].State != StateBackdash {
		t.Fatalf("a double tap back did not backdash: state = %d", s.Players[0].State)
	}
	hold(&s, int(char().BackdashFrames), 0)
	if s.Players[0].X >= start {
		t.Errorf("backdash ended at %d, started at %d", s.Players[0].X, start)
	}
}

// A single tap is a walk. Without this the dash test passes for a recogniser
// that dashes on any forward input at all.
func TestSingleTapDoesNotDash(t *testing.T) {
	s := New()
	s.Advance([2]uint16{InRight, 0})
	if s.Players[0].State != StateWalkF {
		t.Errorf("one tap forward gave state %d, want walk forward", s.Players[0].State)
	}
}

func TestWallsClamp(t *testing.T) {
	s := New()
	run(&s, 1000, [2]uint16{InLeft, InRight})

	for i := range s.Players {
		box := s.Pushbox(i)
		if box.X < -StageHalfWidth || box.X+box.W > StageHalfWidth {
			t.Errorf("player %d pushbox %+v is outside the stage", i, box)
		}
	}
	// And they actually reached the walls rather than stopping early.
	if (s.Pushbox(0).X + StageHalfWidth).Abs() > One {
		t.Errorf("player 0 did not reach the left wall: %+v", s.Pushbox(0))
	}
}

func TestPlayersDoNotMerge(t *testing.T) {
	s := New()
	for f := range 500 {
		s.Advance([2]uint16{InRight, InLeft}) // walk into each other and keep pushing
		a, b := s.Pushbox(0), s.Pushbox(1)
		if a.Overlaps(b) {
			t.Fatalf("frame %d: pushboxes overlap: %+v and %+v", f, a, b)
		}
	}
}

func TestJumpingOverAnotherPlayerIgnoresPushbox(t *testing.T) {
	s := New()
	s.Players[0].X = 0
	s.Players[1].X = 0
	s.Players[1].Y = char().StandHurt.H + One // clear overhead
	before := s.Players[0].X

	s.Advance([2]uint16{})
	if s.Players[0].X != before {
		t.Errorf("player 0 was pushed by a player stacked above it: %d -> %d", before, s.Players[0].X)
	}
}

// The camera is gameplay: a desynced camera is a desynced corner.
func TestCameraFollowsAndStopsAtTheWalls(t *testing.T) {
	s := New()
	if s.CamX != 0 {
		t.Errorf("camera starts at %d, want centred", s.CamX)
	}

	s.Players[0].X = StageHalfWidth
	s.Players[1].X = StageHalfWidth
	s.updateCamera()
	if want := StageHalfWidth - CameraHalfWidth; s.CamX != want && CameraHalfWidth < StageHalfWidth {
		t.Errorf("camera at the right wall = %d, want %d", s.CamX, want)
	}
	if s.CamX+CameraHalfWidth > StageHalfWidth {
		t.Errorf("camera shows past the right wall: %d", s.CamX)
	}
}

// Dash recognition, including the sequences that must NOT dash.
//
// The reported bug: back, forward, back came out as a backdash. Any rule that
// treats the gap as "not the dash direction" does that — the player walked one
// way, walked the other, and got a dash they never asked for. The opposite
// direction is a different intent, not a pause inside one input.
func TestDashRecognition(t *testing.T) {
	// Numpad, from player 0's point of view (it starts facing right).
	const N, F, B, D, U = 5, 6, 4, 2, 8

	for _, tc := range []struct {
		name string
		dirs []uint8
		want int32
	}{
		{"forward, gap, forward", []uint8{F, N, F}, StateDash},
		{"back, gap, back", []uint8{B, N, B}, StateBackdash},
		// Only a clean tap-neutral-tap dashes. A diagonal or a crouch in the
		// middle is the player doing something else, and dashing there is a
		// dash nobody asked for.
		{"forward, down, forward", []uint8{F, D, F}, StateWalkF},
		{"back, down, back", []uint8{B, D, B}, StateWalkB},
		{"forward, down-forward, forward", []uint8{F, 3, F}, StateWalkF},
		{"down-forward twice is not a dash", []uint8{3, N, 3}, StateCrouch},

		// Up in the middle is a jump, not a gap: pre-jump is not actionable, so
		// the third input is correctly ignored. Listed because it looks like it
		// belongs with the cases above and does not.
		{"back, up, back", []uint8{B, U, B}, StatePreJump},

		{"back, forward, back", []uint8{B, F, B}, StateWalkB},
		{"forward, back, forward", []uint8{F, B, F}, StateWalkF},
		{"back, forward, gap, back", []uint8{B, F, N, B}, StateWalkB},

		{"held forward is a walk", []uint8{F, F, F, F}, StateWalkF},
		{"held back is a walk", []uint8{B, B, B, B}, StateWalkB},
		{"one tap is a walk", []uint8{N, F}, StateWalkF},

		// Outside the window the first tap no longer counts.
		{"two taps too far apart", []uint8{F, N, N, N, N, N, N, N, N, N, N, N, F}, StateWalkF},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := New()
			feed(&s, tc.dirs...)
			if got := s.Players[0].State; got != tc.want {
				t.Errorf("%v gave state %d, want %d", tc.dirs, got, tc.want)
			}
		})
	}
}

// Facing is what makes a dash forward or back, so the same physical input must
// dash the other way for the other player.
func TestDashIsFacingRelative(t *testing.T) {
	s := New() // player 1 starts facing left
	for _, d := range []uint8{4, 5, 4} {
		s.Advance([2]uint16{0, pad[d]}) // physically left, which is p1's forward
	}
	if got := s.Players[1].State; got != StateDash {
		t.Errorf("left-left for a left-facing player gave state %d, want dash", got)
	}
}
