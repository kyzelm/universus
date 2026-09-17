// Command replay is the headless replay harness. It runs an input log through
// the native sim and prints a checksum per frame; the same log run through the
// WASM build must print the same bytes. That comparison is the determinism gate.
//
// Log format: universus/sim/replaylog, which owns the header and the frames and
// is the same decoder the server's verification worker runs.
//
// Usage:
//
//	replay gen <frames> <file>   write a deterministic input log
//	replay run <file>            print "<frame> <checksum>" for every frame
package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"

	"universus/data"
	"universus/sim"
	"universus/sim/replaylog"
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

func writeLog(path string, u sim.Setup, in [][2]uint16) error {
	v, err := data.Version()
	if err != nil {
		return err
	}
	return os.WriteFile(path, replaylog.Encode(u, v, in), 0o644)
}

func readLog(path string) (sim.Setup, [][2]uint16, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return sim.Setup{}, nil, err
	}
	log, err := replaylog.Decode(path, b)
	if err != nil {
		return sim.Setup{}, nil, err
	}

	// Recorded, compared, never enforced. A log taken against older frame data
	// still replays; it just replays a match that is no longer the same one,
	// and this line is the explanation waiting for whoever diffs the checksums.
	if v, err := data.Version(); err == nil && v != log.DataVersion {
		fmt.Fprintf(os.Stderr, "replay: %s was recorded against data version %08x, this build is %08x\n",
			path, log.DataVersion, v)
	}
	return log.Setup, log.Inputs, nil
}
