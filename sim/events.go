package sim

// Rollback-safe events (02 Architecture/Rollback Netcode.md, 04 Art/Audio.md).
//
// **The classic rollback bug is a hit sound playing eight times.** It happens
// the obvious way: the sim fires an effect imperatively the moment a hit lands,
// a packet arrives, the frame is rewound and replayed, and the effect fires
// again on every replay. A frame that is rolled back eight times is simulated
// nine times, and the player hears nine punches for one hit.
//
// The fix is the one the design note names, and it is structural rather than
// careful: **the sim sets flags in state and fires nothing.** The flags roll
// back with everything else, so a replayed frame recomputes them rather than
// accumulating them. The view then fires effects only for flags on *confirmed*
// frames — frames whose inputs are all known and which therefore cannot be
// simulated again.
//
// Built in M2 with nothing attached to it, deliberately. There are no sounds
// and no particles yet; retrofitting this once there are means auditing every
// effect in the game, and the bug it prevents is silent, intermittent and only
// reproducible online.
//
// The flags live on the player rather than on the match because almost every
// effect has a position: a hit spark belongs where the fighter is standing.
const (
	// EventHit is a clean hit taken — the strike connected and was not
	// blocked. On the *defender*, like every flag here: effects are drawn on
	// the thing they happened to.
	EventHit int32 = 1 << 0

	// EventBlock is a hit blocked.
	EventBlock int32 = 1 << 1

	// EventThrown is a throw connecting, on the player being thrown.
	EventThrown int32 = 1 << 2

	// EventKnockdown is hitting the floor.
	EventKnockdown int32 = 1 << 3

	// EventSuper is a super activating, on the player who spent the bars. The
	// activation flash the design note wants is this flag plus a view that
	// draws something; the *freeze* it also asks for is a gameplay pause and
	// is not built (see D78).
	EventSuper int32 = 1 << 4
)

// clearEvents wipes both players' flags. Called at the top of every frame, so
// a flag means "this happened on this frame" and never "this happened at some
// point recently" — a flag that outlived its frame would be fired again on
// every confirmed frame it survived into, which is the bug in a slower form.
func (s *GameState) clearEvents() {
	s.Players[0].Events = 0
	s.Players[1].Events = 0
}

// packEvents is both players' flags in one word, player 0 low. What crosses the
// WASM boundary, where a call returning two numbers costs two calls.
func (s *GameState) packEvents() uint32 {
	return uint32(uint16(s.Players[0].Events)) | uint32(uint16(s.Players[1].Events))<<16
}
