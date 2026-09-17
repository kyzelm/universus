// Command replay is the headless replay harness. It runs an input log through
// the native sim and prints a checksum per frame; the same log run through the
// WASM build must print the same bytes. That comparison is the determinism gate.
//
// Log format: a 16-byte header, then little-endian uint16 pairs, player 1 then
// player 2, one pair per frame. The pairs are the same bytes the sim, the
// network and the server see; the header is everything about the match that is
// not in them.
//
//	0  [4] magic "UNIV"
//	4  u8  format version
//	5  u8  training mode
//	6  u8  AI tier in seat 2
//	7  u8  character, player 1
//	8  u8  character, player 2
//	9  [3] reserved, zero
//	12 u32 data version (hash of the embedded character and balance files)
//
// The header exists because the log records what the *caller* fed the sim, and
// two modes generate inputs the caller never sent: the AI's presses come from
// inside Advance, and the lab changes what a frame does. Replaying either
// without its setup replays a different match — a lab session comes back as one
// where seat 2 stands still.
//
// The data version is recorded and compared, never enforced: a log taken
// against older frame data is still a log, and the warning is the explanation
// for the divergence rather than a reason to refuse the file.
//
// Usage:
//
//	replay gen <frames> <file>   write a deterministic input log
//	replay run <file>            print "<frame> <checksum>" for every frame
package main

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"strconv"

	"universus/data"
	"universus/sim"
)

const usage = `usage:
  replay gen <frames> <file>   write a deterministic input log
  replay run <file>            print "<frame> <checksum>" for every frame
  replay dump <file> <frame>   print every state field at a frame, for diffing`

func main() {
	// Both entrypoints load the same embedded roster before touching the sim.
	// The determinism gate compares this binary against the WASM one, so if
	// they disagreed about character data every checksum would differ and the
	// gate would report a divergence with no cause anywhere in the sim.
	if err := loadRoster(); err != nil {
		fmt.Fprintln(os.Stderr, "replay:", err)
		os.Exit(1)
	}
	if err := cli(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "replay:", err)
		os.Exit(1)
	}
}

func loadRoster() error {
	cs, err := data.Load()
	if err != nil {
		return err
	}
	if !sim.LoadCharacters(cs) {
		return fmt.Errorf("sim refused the roster (%d characters)", len(cs))
	}

	b, err := data.LoadBalance()
	if err != nil {
		return err
	}
	sim.LoadBalance(b)
	return nil
}

func cli(args []string) error {
	switch {
	case len(args) == 3 && args[0] == "gen":
		frames, err := strconv.Atoi(args[1])
		if err != nil || frames <= 0 {
			return fmt.Errorf("frame count %q must be a positive integer", args[1])
		}
		return writeLog(args[2], sim.Setup{}, gen(frames))

	case len(args) == 3 && args[0] == "dump":
		frame, err := strconv.Atoi(args[2])
		if err != nil || frame < 0 {
			return fmt.Errorf("frame %q must be a non-negative integer", args[2])
		}
		setup, in, err := readLog(args[1])
		if err != nil {
			return err
		}
		if frame > len(in) {
			return fmt.Errorf("frame %d is past the end of a %d-frame log", frame, len(in))
		}
		s := sim.NewSessionOf(setup)
		for _, i := range in[:frame] {
			s.Advance(i)
		}
		_, err = os.Stdout.WriteString(s.State().Dump())
		return err

	case len(args) == 2 && args[0] == "run":
		setup, in, err := readLog(args[1])
		if err != nil {
			return err
		}
		w := bufio.NewWriter(os.Stdout)
		defer w.Flush()
		return run(setup, in, w)

	default:
		return fmt.Errorf("%s", usage)
	}
}

// run advances one frame per logged input and reports the checksum after each.
// Frame 0 is printed first, so the starting state is compared too.
func run(setup sim.Setup, in [][2]uint16, w io.Writer) error {
	s := sim.NewSessionOf(setup)
	if _, err := fmt.Fprintf(w, "%d %08x\n", s.Frame(), s.Checksum()); err != nil {
		return err
	}
	for _, i := range in {
		s.Advance(i)
		if _, err := fmt.Fprintf(w, "%d %08x\n", s.Frame(), s.Checksum()); err != nil {
			return err
		}
	}
	return nil
}

// gen builds a log from a fixed-seed LCG. Not math/rand: this log has to come
// out byte-identical on any machine and any Go version, and math/rand's
// algorithm is not part of its compatibility promise.
//
// ponytail: both players change input on the same frame every 5 frames, which
// is not how humans play. It exercises jumps, walls and pushboxes, which is all
// the determinism gate needs. Real recorded logs replace it in M1.
func gen(frames int) [][2]uint16 {
	const validBits = 0x077F // bits 0-6 and 8-10; 7 and 11-15 are reserved

	x := uint32(0x9E3779B9)
	next := func() uint16 {
		x = x*1664525 + 1013904223
		return uint16(x>>16) & validBits // high bits: the low ones cycle short
	}

	in := make([][2]uint16, frames)
	var held [2]uint16
	for f := range in {
		if f%5 == 0 {
			held = [2]uint16{next(), next()}
		}
		in[f] = held
	}
	return in
}

// Header layout, documented at the top of the file. Sixteen bytes so the input
// pairs stay 4-byte aligned in the file, and so the reserved bytes have
// somewhere to live if a mode is added later.
const (
	headerSize = 16
	logVersion = 1
)

var magic = [4]byte{'U', 'N', 'I', 'V'}

func writeLog(path string, u sim.Setup, in [][2]uint16) error {
	b := make([]byte, headerSize+len(in)*4)
	copy(b, magic[:])
	b[4] = logVersion
	if u.Training {
		b[5] = 1
	}
	b[6] = byte(u.AI)
	b[7] = byte(u.Chars[0])
	b[8] = byte(u.Chars[1])
	// b[9:12] stay zero: reserved, and a reader checks them.

	v, err := data.Version()
	if err != nil {
		return err
	}
	binary.LittleEndian.PutUint32(b[12:], v)

	for f, p := range in {
		binary.LittleEndian.PutUint16(b[headerSize+f*4:], p[0])
		binary.LittleEndian.PutUint16(b[headerSize+f*4+2:], p[1])
	}
	return os.WriteFile(path, b, 0o644)
}

func readLog(path string) (sim.Setup, [][2]uint16, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return sim.Setup{}, nil, err
	}
	return parseLog(path, b)
}

func parseLog(path string, b []byte) (sim.Setup, [][2]uint16, error) {
	fail := func(format string, a ...any) (sim.Setup, [][2]uint16, error) {
		return sim.Setup{}, nil, fmt.Errorf("%s: "+format, append([]any{path}, a...)...)
	}

	if len(b) < headerSize || [4]byte(b[:4]) != magic {
		return fail("not an input log (no %q header)", magic)
	}
	if b[4] != logVersion {
		return fail("log format version %d, this build reads %d", b[4], logVersion)
	}
	body := b[headerSize:]
	if len(body) == 0 || len(body)%4 != 0 {
		return fail("%d bytes after the header is not a whole number of 4-byte frames", len(body))
	}

	setup := sim.Setup{
		Training: b[5] != 0,
		AI:       int32(b[6]),
		Chars:    [2]int32{int32(b[7]), int32(b[8])},
	}
	if setup.AI > sim.AIHard {
		return fail("AI tier %d is not a difficulty this build knows", setup.AI)
	}
	for _, c := range setup.Chars {
		if c < 0 || c >= sim.NumCharacters() {
			// CharacterAt would hand back the zero character rather than fail,
			// and a match between two fighters who cannot move is a confusing
			// way to learn the log named a character this build does not have.
			return fail("character %d is not in this roster of %d", c, sim.NumCharacters())
		}
	}

	// Recorded, compared, never enforced. A log taken against older frame data
	// still replays; it just replays a match that is no longer the same one,
	// and this line is the explanation waiting for whoever diffs the checksums.
	if v, err := data.Version(); err == nil && v != binary.LittleEndian.Uint32(b[12:]) {
		fmt.Fprintf(os.Stderr, "replay: %s was recorded against data version %08x, this build is %08x\n",
			path, binary.LittleEndian.Uint32(b[12:]), v)
	}

	in := make([][2]uint16, len(body)/4)
	for f := range in {
		in[f] = [2]uint16{
			binary.LittleEndian.Uint16(body[f*4:]),
			binary.LittleEndian.Uint16(body[f*4+2:]),
		}
	}
	return setup, in, nil
}
