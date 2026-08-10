package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A mis-parsed log makes the determinism gate compare the wrong thing and pass,
// which is worse than failing. Pin the round trip and the rejections.
func TestLogRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.inputs")
	want := gen(97)

	if err := writeLog(path, want); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Size() != 97*4 {
		t.Fatalf("log is %d bytes, want %d", fi.Size(), 97*4)
	}

	got, err := readLog(path)
	if err != nil {
		t.Fatal(err)
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

func TestReadLogRejectsPartialFrames(t *testing.T) {
	path := filepath.Join(t.TempDir(), "truncated.inputs")
	if err := os.WriteFile(path, []byte{1, 0, 2}, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readLog(path); err == nil {
		t.Error("a 3-byte log was accepted; frames are 4 bytes")
	}
	if _, err := readLog(filepath.Join(t.TempDir(), "nope.inputs")); err == nil {
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
	if err := run(gen(10), &out); err != nil {
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
	if err := run(gen(10), &again); err != nil {
		t.Fatal(err)
	}
	if out.String() != again.String() {
		t.Error("two runs of the same log printed different checksums")
	}
}
