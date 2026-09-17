package replaylog

import (
	"os"
	"strings"
	"testing"

	"universus/sim"
)

// A roster of two, which is all these tests need from it: the decoder refuses a
// character index the build does not have, and that check needs a size.
func TestMain(m *testing.M) {
	if !sim.LoadCharacters([]sim.Character{{Name: "one"}, {Name: "two"}}) {
		panic("the fixture roster was refused")
	}
	os.Exit(m.Run())
}

func inputs(n int) [][2]uint16 {
	out := make([][2]uint16, n)
	for f := range out {
		out[f] = [2]uint16{uint16(f), uint16(f * 3)}
	}
	return out
}

// A mis-parsed log makes the determinism gate compare the wrong thing and pass,
// which is worse than failing. Pin the round trip.
func TestRoundTrip(t *testing.T) {
	// Not the zero setup: a header that survives only because every field
	// happens to be zero proves nothing.
	setup := sim.Setup{Training: true, AI: sim.AINormal, Chars: [2]int32{1, 0}}
	want := inputs(97)

	b := Encode(setup, 0xdeadbeef, want)
	if len(b) != HeaderSize+97*4 {
		t.Fatalf("encoded %d bytes, want %d", len(b), HeaderSize+97*4)
	}

	got, err := Decode("test", b)
	if err != nil {
		t.Fatal(err)
	}
	if got.Setup != setup {
		t.Errorf("read setup %+v, wrote %+v", got.Setup, setup)
	}
	if got.DataVersion != 0xdeadbeef {
		t.Errorf("read data version %08x", got.DataVersion)
	}
	for f := range want {
		if got.Inputs[f] != want[f] {
			t.Fatalf("frame %d: read %v, wrote %v", f, got.Inputs[f], want[f])
		}
	}
}

// The offsets, asserted literally. The writer is in TypeScript in another
// process, and nothing catches a reader that drifts from it except a test on
// each side pinning the same numbers (client/src/game/log.test.ts).
func TestTheByteLayoutIsTheDocumentedOne(t *testing.T) {
	b := Encode(sim.Setup{Training: true, AI: 2, Chars: [2]int32{1, 0}}, 0x01020304,
		[][2]uint16{{0x0201, 0x0010}})

	if string(b[:4]) != "UNIV" {
		t.Errorf("magic is %q", b[:4])
	}
	for i, want := range map[int]byte{4: Version, 5: 1, 6: 2, 7: 1, 8: 0, 9: 0, 10: 0, 11: 0} {
		if b[i] != want {
			t.Errorf("byte %d is %d, want %d", i, b[i], want)
		}
	}
	if u32(b[12:]) != 0x01020304 {
		t.Errorf("the data version is not at offset 12")
	}
	if b[HeaderSize] != 0x01 || b[HeaderSize+1] != 0x02 {
		t.Errorf("the first input pair is not little-endian at offset %d", HeaderSize)
	}
}

func TestDecodeRefusesWhatItCannotReplay(t *testing.T) {
	good := func() []byte { return Encode(sim.Setup{}, 0, inputs(2)) }

	for _, c := range []struct {
		name string
		log  []byte
	}{
		{"truncated", []byte{1, 0, 2}},
		{"no magic", append(make([]byte, 4), good()[4:]...)},
		{"future version", func() []byte { b := good(); b[4] = Version + 1; return b }()},
		{"partial frame", good()[:HeaderSize+3]},
		{"header only", good()[:HeaderSize]},
		{"unknown tier", func() []byte { b := good(); b[6] = byte(sim.AIHard + 1); return b }()},
		{"character off the roster", func() []byte { b := good(); b[7] = 250; return b }()},
	} {
		if _, err := Decode(c.name, c.log); err == nil {
			t.Errorf("%s: accepted", c.name)
		} else if !strings.Contains(err.Error(), c.name) {
			t.Errorf("%s: the error does not name the log: %v", c.name, err)
		}
	}
}
