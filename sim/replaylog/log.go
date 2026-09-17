// Package replaylog reads and writes the input log format.
//
//	0  [4] magic "UNIV"
//	4  u8  format version
//	5  u8  training mode
//	6  u8  AI tier in seat 2
//	7  u8  character, player 1
//	8  u8  character, player 2
//	9  [3] reserved, zero
//	12 u32 data version (hash of the embedded character and balance files)
//	16 ..  little-endian uint16 pairs, player 1 then player 2, one per frame
//
// **The header exists because the log records what the caller fed the sim**,
// and two modes generate inputs the caller never sent: the AI's presses come
// from inside Advance, and the lab changes what a frame does. Replayed without
// its setup, a lab session comes back as one where seat 2 stands still (D102).
//
// It lives here, in one package, because it now has **two readers** — the
// headless replay harness and the server's verification worker — and a format
// with two implementations is a format with two behaviours. The writer is the
// third, in TypeScript, and is pinned to these offsets by a test on each side.
//
// A subpackage of sim rather than a package beside it: the float ban greps
// ./sim/ recursively, and a decoder for the bytes the sim consumes belongs
// under the same rule as the sim. Like sim itself it has no dependencies and
// hand-rolls its little-endian reads.
package replaylog

import (
	"fmt"

	"universus/sim"
)

const (
	// Sixteen so the input pairs stay 4-byte aligned, and so a mode added later
	// has somewhere to live.
	HeaderSize = 16
	Version    = 1
)

var magic = [4]byte{'U', 'N', 'I', 'V'}

// Log is a decoded input log: how the match was set up, and every frame of it.
type Log struct {
	Setup sim.Setup
	// DataVersion the log was recorded against. **Reported, never enforced**: a
	// log taken against older frame data is still a log, and the mismatch is
	// the explanation for a divergence rather than a reason to refuse the file.
	DataVersion uint32
	Inputs      [][2]uint16
}

func u16(b []byte) uint16 { return uint16(b[0]) | uint16(b[1])<<8 }

func u32(b []byte) uint32 {
	return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24
}

func putU16(b []byte, v uint16) {
	b[0] = byte(v)
	b[1] = byte(v >> 8)
}

func putU32(b []byte, v uint32) {
	b[0] = byte(v)
	b[1] = byte(v >> 8)
	b[2] = byte(v >> 16)
	b[3] = byte(v >> 24)
}

// Encode packs a log. The name is what it is written to, for errors.
func Encode(setup sim.Setup, dataVersion uint32, inputs [][2]uint16) []byte {
	b := make([]byte, HeaderSize+len(inputs)*4)
	copy(b, magic[:])
	b[4] = Version
	if setup.Training {
		b[5] = 1
	}
	b[6] = byte(setup.AI)
	b[7] = byte(setup.Chars[0])
	b[8] = byte(setup.Chars[1])
	// b[9:12] stay zero: reserved, and a reader checks the size that covers them.
	putU32(b[12:], dataVersion)

	for f, p := range inputs {
		putU16(b[HeaderSize+f*4:], p[0])
		putU16(b[HeaderSize+f*4+2:], p[1])
	}
	return b
}

// Decode parses a log and refuses anything it could not replay faithfully.
//
// **A setup naming a character or a difficulty this build does not have is
// refused rather than clamped.** CharacterAt answers an unknown index with the
// zero character, so a clamped log would replay as a match between two fighters
// who cannot move — a confusing way to learn the log was not for this build.
func Decode(name string, b []byte) (Log, error) {
	fail := func(format string, a ...any) (Log, error) {
		return Log{}, fmt.Errorf("%s: "+format, append([]any{name}, a...)...)
	}

	if len(b) < HeaderSize || [4]byte(b[:4]) != magic {
		return fail("not an input log (no %q header)", magic)
	}
	if b[4] != Version {
		return fail("log format version %d, this build reads %d", b[4], Version)
	}
	body := b[HeaderSize:]
	if len(body) == 0 || len(body)%4 != 0 {
		return fail("%d bytes after the header is not a whole number of 4-byte frames", len(body))
	}

	log := Log{
		Setup: sim.Setup{
			Training: b[5] != 0,
			AI:       int32(b[6]),
			Chars:    [2]int32{int32(b[7]), int32(b[8])},
		},
		DataVersion: u32(b[12:]),
	}
	if log.Setup.AI > sim.AIHard {
		return fail("AI tier %d is not a difficulty this build knows", log.Setup.AI)
	}
	for _, c := range log.Setup.Chars {
		if c < 0 || c >= sim.NumCharacters() {
			return fail("character %d is not in this roster of %d", c, sim.NumCharacters())
		}
	}

	log.Inputs = make([][2]uint16, len(body)/4)
	for f := range log.Inputs {
		log.Inputs[f] = [2]uint16{u16(body[f*4:]), u16(body[f*4+2:])}
	}
	return log, nil
}
