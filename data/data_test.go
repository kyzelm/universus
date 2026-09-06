package data

import (
	"bytes"
	"encoding/json"
	"math/bits"
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

	// The full grounded normal set: every button, standing and crouching. A
	// missing one reads in play as a button that does nothing.
	for _, stance := range []int32{sim.StanceStand, sim.StanceCrouch} {
		for _, b := range []uint16{sim.InLP, sim.InMP, sim.InHP, sim.InLK, sim.InMK, sim.InHK} {
			found := false
			for m := int32(0); m < cs[0].NumMoves; m++ {
				mv := &cs[0].Moves[m]
				found = found || (mv.Motion == sim.MotionNone && mv.Stance == stance && mv.Button == b)
			}
			if !found {
				t.Errorf("no normal for stance %d button %#x", stance, b)
			}
		}
	}

	// Frame advantage is computed from the frame data, never authored, so this
	// is a check on the numbers rather than on a field. Nothing may be more
	// than +3 on block: a normal that is safely plus on block with no cost is
	// a button with no answer to it.
	for m := int32(0); m < cs[0].NumMoves; m++ {
		mv := &cs[0].Moves[m]
		if adv := mv.OnBlock(); adv > 3 {
			t.Errorf("move %d is %+d on block", m, adv)
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
		{"launch with one value", func(c *jsonCharacter) { c.Moves[0].Launch = []json.Number{"2"} }},
		{"launch that is not a number", func(c *jsonCharacter) {
			c.Moves[0].Launch = []json.Number{"2", "up"}
		}},
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

		{"empty invuln window", func(c *jsonCharacter) { c.Moves[0].Invuln = []int{4, 4} }},
		{"invuln with one value", func(c *jsonCharacter) { c.Moves[0].Invuln = []int{0} }},
		{"invuln past the end of the move", func(c *jsonCharacter) { c.Moves[0].Invuln = []int{0, 999} }},
		{"negative landing recovery", func(c *jsonCharacter) { c.Moves[0].Landing = -1 }},
		{"knockback with one value", func(c *jsonCharacter) {
			c.Moves[0].Knockback = []json.Number{"2"}
		}},
		{"knockback that is not a number", func(c *jsonCharacter) {
			c.Moves[0].Knockback = []json.Number{"2", "away"}
		}},
		// [0, 0] is the balance default written in a way that reads as "this
		// move pushes nobody", and the two are not the same claim.
		{"a knockback of nothing", func(c *jsonCharacter) {
			c.Moves[0].Knockback = []json.Number{"0", "0"}
		}},
		{"a negative juggle limit", func(c *jsonCharacter) { c.Moves[0].JuggleLimit = -1 }},
		{"unknown cancel category", func(c *jsonCharacter) { c.Moves[0].Cancel = []string{"super4"} }},

		{"a fourth super level", func(c *jsonCharacter) { c.Moves[0].Super = 4 }},
		{"a super with no motion", func(c *jsonCharacter) { c.Moves[0].Input.Motion = "" }},

		{"an unknown button inside a combination", func(c *jsonCharacter) {
			c.Moves[0].Input.Button = "LP+LOL"
		}},
		{"a throw that releases immediately", func(c *jsonCharacter) {
			c.Moves[0].Throw, c.Moves[0].Hitstun = true, 0
		}},
		{"a throw that is also a super", func(c *jsonCharacter) { c.Moves[0].Throw = true }},

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

// A renamed or mistyped JSON key parses to zero without complaint, and the
// symptom is a move that is quietly no longer invincible, no longer cancelable
// and free on landing. Cheap insurance: the shipped roster must actually use
// each of the three.
func TestTheShippedRosterUsesThePerMoveProperties(t *testing.T) {
	cs, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	var invuln, landing, cancel, launcher, ender int
	for i := range cs {
		for m := int32(0); m < cs[i].NumMoves; m++ {
			mv := &cs[i].Moves[m]
			if mv.InvulnEnd > mv.InvulnStart {
				invuln++
			}
			if mv.Landing > 0 {
				landing++
			}
			if mv.CancelInto != 0 {
				cancel++
			}
			if mv.Launcher() {
				launcher++
			}
			if mv.JuggleLimit != 0 {
				ender++
			}
		}
	}
	if invuln == 0 || landing == 0 || cancel == 0 {
		t.Errorf("roster has %d invulnerable, %d landing-recovery and %d cancelable moves, want some of each",
			invuln, landing, cancel)
	}
	// A roster with no launcher is a roster in which the juggle system, the
	// airborne hitstun and the knockdown that ends a juggle are all unreachable
	// — shipped, tested, and dead in play.
	if launcher == 0 || ender == 0 {
		t.Errorf("roster has %d launchers and %d moves with their own juggle limit, want some of each",
			launcher, ender)
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

// The balance file is loaded on the same terms as the roster: it fails here
// rather than at the start of a match, and every field it sets is one the sim
// would otherwise run at zero.
func TestEmbeddedBalanceLoads(t *testing.T) {
	b, err := LoadBalance()
	if err != nil {
		t.Fatalf("the embedded balance does not load: %v", err)
	}

	// A zero here is a renamed or mistyped key, which parses without complaint
	// and shows up in play as a gauge that never moves.
	for _, f := range []struct {
		name string
		v    int32
	}{
		{"drive.regen", b.DriveRegen},
		{"drive.regenWalkForward", b.DriveRegenWalkF},
		{"drive.regenBurnout", b.DriveRegenBurnout},
		{"drive.blockCost", b.DriveBlockCost},
		{"drive.burnoutBlockstun", b.BurnoutBlockstun},
		{"drive.burnoutChipPercent", b.BurnoutChipPercent},
		{"damage.starterLight", b.StarterLight},
		{"damage.starterMedium", b.StarterMedium},
		{"damage.starterHeavy", b.StarterHeavy},
		{"damage.minDamagePercent", b.MinDamagePercent},
		{"damage.counterHitPercent", b.CounterHitPercent},
		{"damage.counterHitstun", b.CounterHitstun},
		{"damage.punishHitstun", b.PunishHitstun},
		{"super.dealtPercent", b.SuperDealtPercent},
		{"super.takenPercent", b.SuperTakenPercent},
		{"super.onSpecial", b.SuperOnSpecial},
	} {
		if f.v <= 0 {
			t.Errorf("%s is %d", f.name, f.v)
		}
	}

	// The shipped numbers have to produce a game, not just a valid file. The
	// design note asks for a Burnout of roughly nine seconds and a Drive gauge
	// that a blocking player can actually empty.
	if f := sim.DriveMax / b.DriveRegenBurnout; f < 300 || f > 720 {
		t.Errorf("Burnout lasts %d frames, want roughly the ~9 seconds the design asks for", f)
	}
	if n := sim.DriveMax / b.DriveBlockCost; n < 8 || n > 40 {
		t.Errorf("%d blocked hits empty the gauge; that is not a pressure system", n)
	}
}

func TestBalanceValidationRejectsBadData(t *testing.T) {
	valid := func(t *testing.T) jsonBalance {
		t.Helper()
		raw, err := files.ReadFile(balanceFile)
		if err != nil {
			t.Fatal(err)
		}
		var jb jsonBalance
		if err := json.Unmarshal(raw, &jb); err != nil {
			t.Fatal(err)
		}
		return jb
	}

	for _, tc := range []struct {
		name string
		edit func(*jsonBalance)
	}{
		{"negative regen", func(b *jsonBalance) { b.Drive.Regen = -1 }},
		{"free blocking", func(b *jsonBalance) { b.Drive.BlockCost = 0 }},
		{"chip over 100%", func(b *jsonBalance) { b.Drive.BurnoutChip = 101 }},
		{"walking in regenerates slower than standing still", func(b *jsonBalance) {
			b.Drive.RegenWalkForward = b.Drive.Regen - 1
		}},
		{"Burnout that never ends", func(b *jsonBalance) { b.Drive.RegenBurnout = 0 }},
		{"Burnout over in a blink", func(b *jsonBalance) { b.Drive.RegenBurnout = sim.DriveMax }},

		{"a hit that pushes nobody", func(b *jsonBalance) { b.Knockback.Hit = "0" }},
		{"a block that pushes nobody", func(b *jsonBalance) { b.Knockback.Block = "0" }},
		{"a push that is not a number", func(b *jsonBalance) { b.Knockback.Hit = "hard" }},
		// At 100 the push never decays and the defender slides for the whole of
		// their stun, which is friction meaning none.
		{"pushback that never decays", func(b *jsonBalance) { b.Knockback.DecayPercent = 100 }},
		// A limit of zero refuses every hit against an airborne opponent, since
		// the counter starts there: anti-airs would stop working and the cause
		// would be one field in this file.
		{"a juggle limit nothing can be hit under", func(b *jsonBalance) { b.Juggle.Limit = 0 }},

		{"a throw nobody can escape", func(b *jsonBalance) { b.Throw.TechFrames = 0 }},
		{"a tech window longer than a throw", func(b *jsonBalance) { b.Throw.TechFrames = 21 }},

		{"a short scaling table", func(b *jsonBalance) { b.Damage.ComboScale = []int{100, 80} }},
		{"scaling that rises", func(b *jsonBalance) { b.Damage.ComboScale[4] = 100 }},
		{"scaling to zero", func(b *jsonBalance) { b.Damage.ComboScale[9] = 0 }},
		{"a light starter that scales less than a heavy", func(b *jsonBalance) {
			b.Damage.StarterLight = b.Damage.StarterHeavy + 1
		}},
		{"a counter hit worth less than a clean one", func(b *jsonBalance) {
			b.Damage.CounterHitPercent = 99
		}},
		{"a punish counter worth fewer frames than a counter", func(b *jsonBalance) {
			b.Damage.PunishHitstun = b.Damage.CounterHitstun - 1
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			jb := valid(t)
			tc.edit(&jb)
			if _, err := jb.convert(); err == nil {
				t.Error("accepted balance data that should have been rejected")
			}
		})
	}
}

// The balance file is part of the simulation's identity: a client that tuned
// the block cost is as desynced as one that tuned a hitbox, and the handshake
// compares one number for both.
func TestVersionCoversTheBalanceFile(t *testing.T) {
	raw, err := files.ReadFile(balanceFile)
	if err != nil {
		t.Fatal(err)
	}

	v, err := Version()
	if err != nil {
		t.Fatal(err)
	}

	h := uint32(2166136261)
	for _, b := range raw {
		h = (h ^ uint32(b)) * 16777619
	}
	if v == h {
		t.Fatal("setup: the version is the balance hash alone")
	}

	// Hashing the roster without it must give a different answer, or the file
	// is embedded and unhashed.
	names, err := filenames()
	if err != nil {
		t.Fatal(err)
	}
	without := uint32(2166136261)
	for _, name := range names {
		r, err := files.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for _, b := range append([]byte(name), r...) {
			without = (without ^ uint32(b)) * 16777619
		}
	}
	if v == without {
		t.Error("the version does not cover balance.json")
	}
}

// Three supers per character, one at each level, each with a motion. A super
// with no motion comes out on a bare button and beats the normal on it for the
// rest of the match, which is why the loader refuses one.
func TestTheShippedRosterHasThreeSupers(t *testing.T) {
	cs, err := Load()
	if err != nil {
		t.Fatal(err)
	}

	for i, c := range cs {
		levels := map[int32]int{}
		for m := int32(0); m < c.NumMoves; m++ {
			mv := &c.Moves[m]
			if mv.Super == 0 {
				continue
			}
			levels[mv.Super]++

			if mv.Motion == sim.MotionNone {
				t.Errorf("character %d: a level %d super with no motion", i, mv.Super)
			}
			if got, want := mv.SuperCost(), mv.Super*sim.BarUnits; got != want {
				t.Errorf("character %d: level %d costs %d, want %d", i, mv.Super, got, want)
			}
			// The design's own table: cheap and low damage at level 1, the
			// round-closer at level 3.
			if mv.Super == 3 && mv.Damage < 2000 {
				t.Errorf("character %d: the level 3 deals %d, which is not a round-closer", i, mv.Damage)
			}
		}

		for lvl := int32(1); lvl <= 3; lvl++ {
			if levels[lvl] != 1 {
				t.Errorf("character %d has %d supers at level %d, want exactly 1", i, levels[lvl], lvl)
			}
		}
	}
}

// The cancel tiering is data, and it is only tiering if the sources differ.
// Level 1 out of cancelable normals, level 3 out of anything — including the
// heavies, which cancel into nothing else.
// The throw is what makes blocking a decision, so the shipped roster has to
// have one — and it has to be on more than one button, since a throw sharing a
// button with a normal would take that button over for the rest of the match.
func TestTheShippedRosterHasAThrow(t *testing.T) {
	cs, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	throws := 0
	for _, c := range cs {
		for m := int32(0); m < c.NumMoves; m++ {
			mv := &c.Moves[m]
			if !mv.IsThrow() {
				continue
			}
			throws++
			if bits.OnesCount16(mv.Button) < 2 {
				t.Errorf("the throw is on one button (%#x)", mv.Button)
			}
			if mv.Hitstun <= 0 {
				t.Error("the throw has no hitstun")
			}
		}
	}
	if throws != len(cs) {
		t.Errorf("%d throws across %d characters, want one each", throws, len(cs))
	}
}

func TestSuperCancelsAreTieredBySource(t *testing.T) {
	cs, err := Load()
	if err != nil {
		t.Fatal(err)
	}

	for i, c := range cs {
		var feedsL1, feedsL3Only, feedsFromSpecial int
		for m := int32(0); m < c.NumMoves; m++ {
			mv := &c.Moves[m]
			switch {
			case mv.CancelInto&sim.CancelSuper1 != 0:
				feedsL1++
			case mv.CancelInto == sim.CancelSuper3:
				feedsL3Only++
			}
			if mv.Motion != sim.MotionNone && mv.Super == 0 && mv.CancelInto&sim.CancelSuper2 != 0 {
				feedsFromSpecial++
			}
		}

		if feedsL1 == 0 {
			t.Errorf("character %d: nothing cancels into the level 1", i)
		}
		if feedsL3Only == 0 {
			t.Errorf("character %d: no move reaches only the level 3, so the tiering is not tiered", i)
		}
		if feedsFromSpecial == 0 {
			t.Errorf("character %d: no special cancels into the level 2", i)
		}
	}
}

// The damage pipeline's numbers. The structure is the thesis content and the
// values are a starting point, but a value that is structurally wrong — a
// scaling table that rises, a counter hit worth less than a clean one — is a
// balance bug the loader can catch for free.
func TestTheShippedScalingTable(t *testing.T) {
	b, err := LoadBalance()
	if err != nil {
		t.Fatal(err)
	}

	if b.ComboScale[0] != 100 {
		t.Errorf("the first hit of a combo scales to %d%%, want 100", b.ComboScale[0])
	}
	// The floor is the last entry, and it is what every hit past the table
	// takes. Zero there is the degenerate-loop bug the design note warns about.
	if floor := b.ComboScale[sim.ComboScaleSteps-1]; floor <= 0 || floor > 50 {
		t.Errorf("the scaling floor is %d%%, want a small positive percentage", floor)
	}
	for i := 1; i < sim.ComboScaleSteps; i++ {
		if b.ComboScale[i] > b.ComboScale[i-1] {
			t.Errorf("comboScale rises at %d: %d after %d", i, b.ComboScale[i], b.ComboScale[i-1])
		}
	}

	if b.StarterLight >= b.StarterHeavy {
		t.Errorf("a light starter scales to %d%% and a heavy to %d%%: starter scaling does nothing",
			b.StarterLight, b.StarterHeavy)
	}
	if b.CounterHitPercent <= 100 {
		t.Errorf("a counter hit pays %d%%, which is not a bonus", b.CounterHitPercent)
	}
	if b.CounterHitstun <= 0 {
		t.Error("a counter hit carries no extra hitstun, so it cannot open anything")
	}
}
