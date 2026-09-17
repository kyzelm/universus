package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"universus/sim"
)

// The roster and the balance data are part of the simulation's identity, and
// main loads them before anything touches the sim. A test binary has its own
// entrypoint, so it has to do the same or every match is played by characters
// who cannot move.
func TestMain(m *testing.M) {
	if err := loadRoster(); err != nil {
		fmt.Fprintln(os.Stderr, "replay:", err)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

// The file half of the round trip: the tool writes a log and reads it back,
// stamped with the data version this build was compiled against. The byte
// layout itself is pinned in universus/sim/replaylog, which owns it.
func TestLogRoundTripThroughAFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.inputs")
	want := gen(97)
	setup := sim.Setup{Training: true, AI: sim.AINormal}

	if err := writeLog(path, setup, want); err != nil {
		t.Fatal(err)
	}
	gotSetup, got, err := readLog(path)
	if err != nil {
		t.Fatal(err)
	}
	if gotSetup != setup {
		t.Fatalf("read setup %+v, wrote %+v", gotSetup, setup)
	}
	if len(got) != len(want) {
		t.Fatalf("read %d frames, wrote %d", len(got), len(want))
	}
	for f := range want {
		if got[f] != want[f] {
			t.Fatalf("frame %d: read %v, wrote %v", f, got[f], want[f])
		}
	}

	// A missing file is an error rather than an empty log.
	if _, _, err := readLog(filepath.Join(t.TempDir(), "nope.inputs")); err == nil {
		t.Error("a missing log was accepted")
	}
}

// The whole reason the header exists: the same inputs replayed under a
// different setup are a different match. If these checksums ever matched, the
// setup would not be reaching the sim and a lab log would silently replay as a
// normal one.
func TestSetupChangesWhatTheSameInputsReplayAs(t *testing.T) {
	in := gen(240)

	var normal, lab, ai bytes.Buffer
	for _, c := range []struct {
		out   *bytes.Buffer
		setup sim.Setup
	}{
		{&normal, sim.Setup{}},
		{&lab, sim.Setup{Training: true}},
		{&ai, sim.Setup{AI: sim.AIHard}},
	} {
		if err := run(c.setup, in, c.out); err != nil {
			t.Fatal(err)
		}
	}

	if normal.String() == lab.String() {
		t.Error("training mode replayed identically to a normal match")
	}
	if normal.String() == ai.String() {
		t.Error("an AI match replayed identically to one with an empty seat 2")
	}
}

func TestGenIsStableAndUsesOnlyValidBits(t *testing.T) {
	a, b := gen(200), gen(200)
	for f := range a {
		if a[f] != b[f] {
			t.Fatalf("frame %d differs between two gen calls: %v vs %v", f, a[f], b[f])
		}
		for p, in := range a[f] {
			if in&^0x077F != 0 {
				t.Fatalf("frame %d player %d set a reserved bit: %#x", f, p, in)
			}
		}
	}
}

func TestRunPrintsAChecksumPerFrameStartingAtZero(t *testing.T) {
	var out bytes.Buffer
	if err := run(sim.Setup{}, gen(10), &out); err != nil {
		t.Fatal(err)
	}

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 11 {
		t.Fatalf("got %d lines for 10 frames, want 11 (frame 0 included)", len(lines))
	}
	for f, l := range lines {
		var frame int
		var sum uint32
		if n, _ := fmt.Sscanf(l, "%d %x", &frame, &sum); n != 2 || frame != f {
			t.Errorf("line %d = %q, want %q", f, l, "<frame> <checksum>")
		}
	}

	// Same log, same output — the property the whole gate rests on.
	var again bytes.Buffer
	if err := run(sim.Setup{}, gen(10), &again); err != nil {
		t.Fatal(err)
	}
	if out.String() != again.String() {
		t.Error("two runs of the same log printed different checksums")
	}
}
