package sim

// Rollback parameters, GGPO-standard: 2 frames of input delay (the net layer's
// concern), 8 frames of maximum rollback. The ring holds 12 so the window has
// slack, and it is allocated once — Session is a value, not a pointer soup.
const (
	MaxRollback = 8
	ringSize    = 12

	// The event ring is longer than the state ring on purpose. A state is only
	// ever needed inside the rollback window; an *event* is needed until the
	// view has drawn it, and the view runs on the display's clock — one slow
	// frame must not lose a hit spark. 64 entries is a second of slack for
	// 512 bytes.
	eventRing = 64
)

// Session drives the sim for rollback. It owns the state, a ring of snapshots
// and the inputs that produced them.
//
// None of this is gameplay state, and it deliberately must *not* roll back —
// which is exactly why it lives out here and not in GameState.
type Session struct {
	state  GameState
	ring   [ringSize]GameState // ring[f%ringSize] is the state at the start of frame f
	inputs [ringSize][2]uint16 // inputs[f%ringSize] is what advanced frame f

	// events[f%eventRing] is what happened on frame f, packed (see events.go).
	// A replay overwrites the slot with the corrected value, which is the whole
	// point: the view reads this only for frames that can no longer change, so
	// what it reads is what actually happened rather than what was predicted.
	//
	// The frame is stored beside the flags because the ring wraps. Without it
	// a frame that has fallen out returns some *other* frame's events, which
	// would be a hit spark on a frame nothing happened.
	events [eventRing]struct {
		frame uint32
		bits  uint32
	}
}

func NewSession() Session { return Session{state: New()} }

func (s *Session) State() *GameState { return &s.state }
func (s *Session) Frame() uint32     { return s.state.Frame }
func (s *Session) Checksum() uint32  { return s.state.Checksum() }

// Advance runs one frame, recording what it took to get there.
func (s *Session) Advance(in [2]uint16) {
	i := s.state.Frame % ringSize
	s.ring[i] = s.state
	s.inputs[i] = in

	f := s.state.Frame
	s.state.Advance(in)

	// Recorded after the frame ran and keyed by the frame it belongs to, so a
	// replay of frame f writes the same slot again rather than a new one.
	e := &s.events[f%eventRing]
	e.frame, e.bits = f, s.state.packEvents()
}

// EventsAt is what happened on a frame, packed — player 0 in the low 16 bits.
// Zero for a frame that has fallen out of the ring, which is the right answer:
// an effect nobody drew in a second of wall-clock time is one nobody should be
// shown now.
func (s *Session) EventsAt(frame uint32) uint32 {
	e := &s.events[frame%eventRing]
	if e.frame != frame {
		return 0
	}
	return e.bits
}

// Rewind restores the state at the start of frame. The caller then replays,
// which is what lets it supply corrected inputs for every frame after the
// misprediction and not just the one that was wrong — a packet carrying eight
// frames of history can invalidate all eight.
//
// Reports false when frame is outside the rollback window. That is a desync the
// caller has to handle; silently clamping here would hide it.
func (s *Session) Rewind(frame uint32) bool {
	now := s.state.Frame
	if frame >= now || now-frame > MaxRollback {
		return false
	}
	s.state = s.ring[frame%ringSize] // the rewind is one struct assignment
	return true
}

// Adjust is the single-frame case: the real input for frame turned out to be
// in, so rewind and replay from the inputs already recorded. The net layer uses
// Rewind directly because it has better inputs than the recorded ones.
func (s *Session) Adjust(frame uint32, in [2]uint16) bool {
	now := s.state.Frame
	if !s.Rewind(frame) {
		return false
	}
	s.inputs[frame%ringSize] = in

	for s.state.Frame < now {
		s.Advance(s.inputs[s.state.Frame%ringSize])
	}
	return true
}
