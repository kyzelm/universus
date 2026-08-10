package sim

import "unsafe"

// Save and load need no code: GameState is flat and comparable, so `saved := s`
// and `s = saved` already are the memcpy. The ring buffer is a [12]GameState.

const (
	fnvOffset = 2166136261
	fnvPrime  = 16777619
)

func fnv1a(b []byte) uint32 {
	h := uint32(fnvOffset)
	for _, c := range b {
		h ^= uint32(c)
		h *= fnvPrime
	}
	return h
}

// Checksum is FNV-1a over the raw bytes of the state. Two machines running the
// same inputs must produce the same value on every frame; the differential test
// compares nothing else.
//
// Hashing the struct's own memory is only sound because every field is a 4-byte
// type, so there is no implicit padding and no uninitialised byte to leak in.
// TestGameStateIsFlatAndFixedSize pins that; if it ever fails, this is why.
//
// ponytail: no separate packed representation. It would cost a copy per frame
// and a second layout to keep in sync, and the WriteSnapshot layout is not the
// full state. Add one only if a field ever needs a type narrower than 4 bytes.
func (s *GameState) Checksum() uint32 {
	return fnv1a(unsafe.Slice((*byte)(unsafe.Pointer(s)), unsafe.Sizeof(*s)))
}
