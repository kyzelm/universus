// Command replay is the headless replay harness. It runs an input log through
// the native sim and prints a checksum per frame; the same log run through the
// WASM build must print the same bytes. That comparison is the determinism gate.
//
// Log format: little-endian uint16 pairs, player 1 then player 2, one pair per
// frame. No header — file size / 4 is the frame count. The same bytes the sim,
// the network and the server see.
//
// ponytail: no header, no version field. M0 is throwaway and its GameState will
// not survive M1, so no M0 log is worth replaying later. Add a header (data
// version hash, seed, characters) when the first log worth keeping is recorded.
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
  replay run <file>            print "<frame> <checksum>" for every frame`

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
	return nil
}

func cli(args []string) error {
	switch {
	case len(args) == 3 && args[0] == "gen":
		frames, err := strconv.Atoi(args[1])
		if err != nil || frames <= 0 {
			return fmt.Errorf("frame count %q must be a positive integer", args[1])
		}
		return writeLog(args[2], gen(frames))

	case len(args) == 2 && args[0] == "run":
		in, err := readLog(args[1])
		if err != nil {
			return err
		}
		w := bufio.NewWriter(os.Stdout)
		defer w.Flush()
		return run(in, w)

	default:
		return fmt.Errorf("%s", usage)
	}
}

// run advances one frame per logged input and reports the checksum after each.
// Frame 0 is printed first, so the starting state is compared too.
func run(in [][2]uint16, w io.Writer) error {
	s := sim.NewSession()
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

func writeLog(path string, in [][2]uint16) error {
	b := make([]byte, len(in)*4)
	for f, p := range in {
		binary.LittleEndian.PutUint16(b[f*4:], p[0])
		binary.LittleEndian.PutUint16(b[f*4+2:], p[1])
	}
	return os.WriteFile(path, b, 0o644)
}

func readLog(path string) ([][2]uint16, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(b) == 0 || len(b)%4 != 0 {
		return nil, fmt.Errorf("%s: %d bytes is not a whole number of 4-byte frames", path, len(b))
	}

	in := make([][2]uint16, len(b)/4)
	for f := range in {
		in[f] = [2]uint16{
			binary.LittleEndian.Uint16(b[f*4:]),
			binary.LittleEndian.Uint16(b[f*4+2:]),
		}
	}
	return in, nil
}
