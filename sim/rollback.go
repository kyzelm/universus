package sim

// Rollback parameters, GGPO-standard: 2 frames of input delay (the net layer's
// concern), 8 frames of maximum rollback. The ring holds 12 so the window has
// slack, and it is allocated once — Session is a value, not a pointer soup.
const (
	MaxRollback = 8
	ringSize    = 12
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
	s.state.Advance(in)
}

// Adjust corrects a mispredicted input: the real input for frame turned out to
// be in, so rewind to the start of that frame and replay forward to the frame
// we were already on. This is all of rollback. Predicting, and deciding when to
// call this, belong to the net layer.
//
// Reports false when frame is outside the rollback window. That is a desync the
// caller has to handle — silently clamping here would hide it.
func (s *Session) Adjust(frame uint32, in [2]uint16) bool {
	now := s.state.Frame
	if frame >= now || now-frame > MaxRollback {
		return false
	}

	s.state = s.ring[frame%ringSize] // the rewind is one struct assignment
	s.inputs[frame%ringSize] = in

	for s.state.Frame < now {
		s.Advance(s.inputs[s.state.Frame%ringSize])
	}
	return true
}
