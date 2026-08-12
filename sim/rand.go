package sim

// Deterministic randomness. The generator's entire state is a GameState field,
// so it is saved, restored and checksummed with everything else: a rollback
// rewinds the sequence, and a replay reproduces every random decision exactly.
// An RNG living outside the state is the textbook version of the bug the
// no-gameplay-state-outside-GameState invariant exists to prevent.
//
// xorshift32 (Marsaglia 2003): three shifts and three xors, no multiply, no
// table, no division, period 2^32-1. Gameplay randomness here is cosmetic
// variance and tie-breaking, never balance — this is far more than adequate,
// and being trivially portable is the property that actually matters.
//
// ponytail: not math/rand. Its global source is shared mutable state the sim
// must never touch, rand.Rand carries a pointer, and its algorithm is not
// pinned across Go versions — which would surface as a desync between two
// clients built with different toolchains, months later, once.
//
// 32-bit deliberately. A uint64 field would give GameState 8-byte alignment,
// and every narrow field after it would need hand-computed padding to keep the
// struct free of implicit padding — see the note on Checksum. Staying 4-byte
// throughout means adding a field can never silently introduce an
// uninitialised byte. A wider generator buys nothing this game can use.

// seed is any nonzero constant — the golden-ratio one, chosen only because it
// has a well-mixed bit pattern. M2 replaces it with a value agreed in the match
// handshake, so both peers generate the same sequence from frame 0.
const seed uint32 = 0x9E3779B9

// Rand returns the next value in the sequence and advances the state.
func (s *GameState) Rand() uint32 {
	x := s.RNG
	// Zero is xorshift's fixed point: it would return zero forever. Only
	// reachable from a hand-built GameState{}, since New seeds it — but a
	// silently dead RNG is worth one branch to make impossible.
	if x == 0 {
		x = seed
	}
	x ^= x << 13
	x ^= x >> 17
	x ^= x << 5
	s.RNG = x
	return x
}

// RandN returns a value in [0, n).
//
// Lemire's multiply-shift rather than `% n`: one multiply, no division, and no
// bias toward low values. The uint64 is an intermediate for the high half of a
// 32x32 product — integer throughout, nothing float-adjacent about it.
func (s *GameState) RandN(n uint32) uint32 {
	if n == 0 {
		return 0 // rather than dividing by zero, which is what % would do
	}
	return uint32((uint64(s.Rand()) * uint64(n)) >> 32)
}
