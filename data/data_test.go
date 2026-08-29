package data

import (
	"bytes"
	"encoding/json"
	"testing"

	"universus/sim"
)

// The embedded roster must actually load. A character file that fails
// validation fails the build's most basic promise, and it fails here rather
// than at the start of a match.
func TestEmbeddedRosterLoads(t *testing.T) {
	cs, err := Load()
	if err != nil {
		t.Fatalf("the embedded roster does not load: %v", err)
	}
	if len(cs) == 0 {
		t.Fatal("no characters loaded")
	}
	if !sim.LoadCharacters(cs) {
		t.Fatal("sim refused the loaded roster")
	}

	for i, c := range cs {
		if c.Health <= 0 || c.WalkForward <= 0 || c.NumMoves == 0 {
			t.Errorf("character %d looks empty: %+v", i, c)
		}
		for m := int32(0); m < c.NumMoves; m++ {
			if c.Moves[m].Total() <= 0 {
				t.Errorf("character %d move %d has no duration", i, m)
			}
		}
	}

	// The strength variants are the data-driven claim in miniature: three moves
	// that differ only in numbers, sharing one motion and one code path.
	strengths := map[uint16]bool{}
	for m := int32(0); m < cs[0].NumMoves; m++ {
		if mv := &cs[0].Moves[m]; mv.Motion == sim.MotionQCF {
			strengths[mv.Button] = true
		}
	}
	if len(strengths) != 3 {
		t.Errorf("the fireball has %d strength variants, want 3", len(strengths))
	}
}

// The handshake compares this. If it were not stable across calls, every match
// would be refused; if it did not change with the data, none would be.
func TestVersionIsStableAndCoversTheData(t *testing.T) {
	a, err := Version()
	if err != nil {
		t.Fatal(err)
	}
	b, err := Version()
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Errorf("Version is not stable: %d then %d", a, b)
	}
	if a == 0 {
		t.Error("Version is 0, which is what an empty roster would produce")
	}
}

// The exact values the design notes quote, so a change to the conversion is
// caught here rather than as a subtly different jump arc.
func TestParseFixMatchesTheDocumentedConstants(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want sim.Fix
	}{
		{"1.5", 98304},     // WalkSpeed
		{"8.0", 524288},    // JumpVelocity
		{"-0.375", -24576}, // Gravity
		{"0", 0},
		{"1", 65536},
		{"-1", -65536},
		{"0.5", 32768},
		{"-0.5", -32768},
		{"40", 2621440},
		{"0.0000152587890625", 1}, // exactly 1/65536
	} {
		got, err := parseFix(json.Number(tc.in))
		if err != nil {
			t.Errorf("parseFix(%q): %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("parseFix(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestParseFixRejectsGarbage(t *testing.T) {
	for _, in := range []string{"", "abc", "1.2.3", "1.", "--1", "999999999", "-999999999"} {
		if got, err := parseFix(json.Number(in)); err == nil {
			t.Errorf("parseFix(%q) = %d, want an error", in, got)
		}
	}
}

// Rounding is half away from zero, in integers, and symmetric — an asymmetric
// rounding rule would make a character's forward and back walk speeds differ
// by a bit for no reason anyone could find.
func TestParseFixRoundsSymmetrically(t *testing.T) {
	pos, err := parseFix(json.Number("0.00001"))
	if err != nil {
		t.Fatal(err)
	}
	neg, err := parseFix(json.Number("-0.00001"))
	if err != nil {
		t.Fatal(err)
	}
	if pos != -neg {
		t.Errorf("0.00001 -> %d but -0.00001 -> %d", pos, neg)
	}
}

// Validation exists to catch authoring mistakes loudly. Each case below is a
// real mistake someone makes: the numbers look fine individually.
func TestValidationRejectsBadData(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*jsonCharacter)
	}{
		{"zero health", func(c *jsonCharacter) { c.Health = 0 }},
		{"positive gravity", func(c *jsonCharacter) { c.Gravity = "0.375" }},
		{"no moves", func(c *jsonCharacter) { c.Moves = nil }},
		{"zero startup", func(c *jsonCharacter) { c.Moves[0].Startup = 0 }},
		{"zero active", func(c *jsonCharacter) { c.Moves[0].Active = 0 }},
		{"negative recovery", func(c *jsonCharacter) { c.Moves[0].Recovery = -1 }},
		{"unknown button", func(c *jsonCharacter) { c.Moves[0].Input.Button = "LOL" }},
		{"unknown stance", func(c *jsonCharacter) { c.Moves[0].Input.Stance = "hover" }},
		{"unknown motion", func(c *jsonCharacter) { c.Moves[0].Input.Motion = "360" }},
		{"unknown level", func(c *jsonCharacter) { c.Moves[0].AttackLevel = "sideways" }},
		{"zero-size box", func(c *jsonCharacter) { c.Pushbox = []json.Number{"0", "0", "0", "48"} }},
		{"box with too few values", func(c *jsonCharacter) { c.StandHurt = []json.Number{"0", "0"} }},
		{"no keyframes", func(c *jsonCharacter) { c.Moves[0].Boxes = nil }},
		{"keyframes out of order", func(c *jsonCharacter) {
			c.Moves[0].Boxes[0].Frame, c.Moves[0].Boxes[1].Frame = 4, 0
		}},
		{"keyframe past the end of the move", func(c *jsonCharacter) {
			c.Moves[0].Boxes[2].Frame = 999
		}},
		{"hitbox outside the active window", func(c *jsonCharacter) {
			c.Moves[0].Boxes[1].Hit = nil
			c.Moves[0].Boxes[0].Hit = [][]json.Number{{"12", "30", "22", "12"}}
		}},

		// The one the design note itself got wrong: 8.0 and -0.03 pass every
		// individual check and give a nine-second jump.
		{"jump constants that do not go together", func(c *jsonCharacter) { c.Gravity = "-0.03" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			jc := validCharacter(t)
			tc.edit(&jc)
			if _, err := jc.convert(); err == nil {
				t.Error("accepted data that should have been rejected")
			}
		})
	}
}

// validCharacter is the embedded file, re-parsed, so the test data cannot drift
// away from what actually ships.
func validCharacter(t *testing.T) jsonCharacter {
	t.Helper()

	raw, err := files.ReadFile("characters/00-shoto.json")
	if err != nil {
		t.Fatal(err)
	}
	var jc jsonCharacter
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&jc); err != nil {
		t.Fatal(err)
	}
	if _, err := jc.convert(); err != nil {
		t.Fatalf("the shipped character does not validate: %v", err)
	}
	return jc
}
