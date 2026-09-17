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

// A mis-parsed log makes the determinism gate compare the wrong thing and pass,
// which is worse than failing. Pin the round trip and the rejections.
func TestLogRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.inputs")
	want := gen(97)
	// Not the zero setup: a header that survives the round trip only because
	// every field happens to be zero proves nothing.
	setup := sim.Setup{Training: true, AI: sim.AINormal}

	if err := writeLog(path, setup, want); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Size() != headerSize+97*4 {
		t.Fatalf("log is %d bytes, want %d", fi.Size(), headerSize+97*4)
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
}

// The whole reason the header exists: the same inputs replayed under a
// different setup are a different match. If these checksums ever matched, the
// setup would not be reaching the sim and a lab log would silently replay as a
// normal one — which is the bug this format was added to close.
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

func TestReadLogRejectsWhatItCannotReplay(t *testing.T) {
	dir := t.TempDir()
	good := func() []byte {
		b := make([]byte, headerSize+8)
		copy(b, magic[:])
		b[4] = logVersion
		return b
	}

	for _, c := range []struct {
		name string
		log  []byte
	}{
		{"truncated", []byte{1, 0, 2}},
		{"no magic", append(make([]byte, 4), good()[4:]...)},
		{"future version", func() []byte { b := good(); b[4] = logVersion + 1; return b }()},
		{"partial frame", good()[:headerSize+3]},
		{"header only", good()[:headerSize]},
		{"unknown tier", func() []byte { b := good(); b[6] = byte(sim.AIHard + 1); return b }()},
		{"character off the roster", func() []byte { b := good(); b[7] = 250; return b }()},
	} {
		path := filepath.Join(dir, c.name)
		if err := os.WriteFile(path, c.log, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, _, err := readLog(path); err == nil {
			t.Errorf("%s: accepted", c.name)
		}
	}

	if _, _, err := readLog(filepath.Join(dir, "nope.inputs")); err == nil {
		t.Error("a missing log was accepted")
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
