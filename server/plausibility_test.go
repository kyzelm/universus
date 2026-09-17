package main

import (
	"testing"

	"universus/sim"
	"universus/sim/replaylog"
)

// logOf packs a per-frame input pattern into a real log, so the checks are run
// against exactly the bytes a client would upload.
func logOf(t *testing.T, frames int, press func(f int) [2]uint16) replaylog.Log {
	t.Helper()
	version := loadRoster()

	inputs := make([][2]uint16, frames)
	for f := range inputs {
		inputs[f] = press(f)
	}
	parsed, err := replaylog.Decode("test", replaylog.Encode(sim.Setup{}, version, inputs))
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func kinds(found []anomaly, seat int) map[string]anomaly {
	out := map[string]anomaly{}
	for _, a := range found {
		if a.Seat == seat {
			out[a.Kind] = a
		}
	}
	return out
}

// **An honest match must not be flagged**, and this is the test that matters
// most: every check here is probabilistic, and a false positive costs a real
// player a place in a review queue they did not earn.
func TestAPlayedMatchIsNotFlagged(t *testing.T) {
	logBytes, _, _, _ := playedMatch(t)
	parsed, err := replaylog.Decode("test", logBytes)
	if err != nil {
		t.Fatal(err)
	}

	if found := plausibility(parsed); len(found) > 0 {
		t.Errorf("an ordinary match was flagged: %+v", found)
	}
}

// A window of inputs repeated byte for byte. The one check that needs no
// simulation, and the hardest to argue with: a hand does not press the same
// twenty frames twice, let alone three times.
func TestAMacroIsCaught(t *testing.T) {
	// A 40-frame loop with movement and a button in it, played five times.
	loop := []uint16{
		sim.InRight, sim.InRight, sim.InDown, sim.InDown | sim.InRight,
		sim.InRight | sim.InLP, 0, 0, 0,
	}
	parsed := logOf(t, 600, func(f int) [2]uint16 {
		return [2]uint16{loop[f%len(loop)], 0}
	})

	got := kinds(plausibility(parsed), 1)
	a, ok := got["macro"]
	if !ok {
		t.Fatalf("a looping macro was not caught: %+v", got)
	}
	if a.Value < macroRepeats {
		t.Errorf("reported %v repeats", a.Value)
	}

	// Seat 2 pressed nothing at all and must not be flagged for it.
	if len(kinds(plausibility(parsed), 2)) > 0 {
		t.Error("an idle seat was flagged")
	}
}

// Holding a direction repeats forever and says nothing about anybody.
func TestHoldingADirectionIsNotAMacro(t *testing.T) {
	parsed := logOf(t, 600, func(int) [2]uint16 { return [2]uint16{sim.InRight, 0} })
	if found := plausibility(parsed); len(found) > 0 {
		t.Errorf("walking forward was flagged: %+v", found)
	}
}

// Randomised-looking input with real variance is what a person produces, and
// it must clear every threshold.
func TestVariedInputClearsTheChecks(t *testing.T) {
	x := uint32(0x9E3779B9)
	next := func() uint16 {
		x = x*1664525 + 1013904223
		return uint16(x>>16) & 0x077F
	}
	held := [2]uint16{}
	parsed := logOf(t, 3000, func(f int) [2]uint16 {
		if f%7 == 0 {
			held = [2]uint16{next(), next()}
		}
		return held
	})

	for _, a := range plausibility(parsed) {
		if a.Kind == "macro" {
			t.Errorf("varied input was called a macro: %+v", a)
		}
	}
}

// The thresholds are the policy, so they are asserted rather than assumed: a
// median reaction under the human floor is what the check is for, and the
// floor is 200 ms at 60 Hz.
func TestTheThresholdsAreTheOnesTheNoteArguesFor(t *testing.T) {
	if reactionFloor != 12 {
		t.Errorf("the reaction floor is %d frames, want 12 (~200 ms at 60 Hz)", reactionFloor)
	}
	if reactionMin < 10 || consistencySample < 10 {
		t.Error("a threshold with a single-digit sample behind it is noise")
	}
}

func TestMedianAndDeviation(t *testing.T) {
	if got := median([]int{5, 1, 3}); got != 3 {
		t.Errorf("median of an odd count is %v", got)
	}
	if got := median([]int{1, 2, 3, 4}); got != 2.5 {
		t.Errorf("median of an even count is %v", got)
	}
	// Identical observations are what a script produces and the check turns on.
	if got := stddev([]int{7, 7, 7, 7}); got != 0 {
		t.Errorf("four identical values deviate by %v", got)
	}
	if stddev([]int{3, 9, 1, 12}) < 1 {
		t.Error("scattered values reported as consistent")
	}
	// One observation cannot deviate from anything, and must not read as
	// perfectly consistent.
	if !isInf(stddev([]int{4})) {
		t.Error("a single observation was called consistent")
	}
}

func isInf(f float64) bool { return f > 1e300 }

// botLog plays a match where one seat is driven by a rule rather than a hand,
// and returns the log a client would have uploaded. The checks below need a bot
// to catch, and a bot is the only honest way to produce one.
func botLog(t *testing.T, frames int, decide func(st *sim.GameState, f int) [2]uint16) replaylog.Log {
	t.Helper()
	version := loadRoster()

	setup := sim.Setup{Chars: [2]int32{0, 1}}
	s := sim.NewSessionOf(setup)
	inputs := make([][2]uint16, 0, frames)
	for f := range frames {
		in := decide(s.State(), f)
		s.Advance(in)
		inputs = append(inputs, in)
	}

	parsed, err := replaylog.Decode("test", replaylog.Encode(setup, version, inputs))
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

// **The check that catches something reading state rather than a screen.** A
// bot blocking three frames after an attack begins is doing it faster than a
// person can see the attack, every single time.
func TestABotThatBlocksFasterThanSightIsFlagged(t *testing.T) {
	// Seat 1 attacks on a loop with a human-looking scatter; seat 2 blocks
	// three frames after each attack starts and neutral otherwise.
	sawAttackAt, wasAttacking := -1, false
	parsed := botLog(t, 6000, func(st *sim.GameState, f int) [2]uint16 {
		attacker, defender := &st.Players[0], &st.Players[1]

		// The attacker's pattern varies so the defender's reactions are
		// separate observations rather than one repeated situation.
		p1 := uint16(sim.InRight)
		if f%(37+f%11) < 2 {
			p1 = sim.InRight | sim.InMP
		}

		attacking := attacker.State == sim.StateAttack
		if attacking && !wasAttacking {
			sawAttackAt = f
		}
		wasAttacking = attacking
		p2 := uint16(0)
		if sawAttackAt >= 0 && f-sawAttackAt >= 3 && f-sawAttackAt < 25 {
			// Away, from this seat's own facing.
			if defender.Facing > 0 {
				p2 = sim.InLeft
			} else {
				p2 = sim.InRight
			}
		}
		return [2]uint16{p1, p2}
	})

	got := kinds(plausibility(parsed), 2)
	a, ok := got["reaction"]
	if !ok {
		t.Fatalf("a three-frame reaction was not flagged: %+v", got)
	}
	if a.Value >= reactionFloor {
		t.Errorf("reported a median of %.1f frames", a.Value)
	}
	if a.Sample < reactionMin {
		t.Errorf("flagged on %d observations, below the minimum of %d", a.Sample, reactionMin)
	}
}

// **The check that catches a bot deliberately slow enough to pass the reaction
// floor.** It presses late, but it presses late by exactly the same amount
// every time, and a hand does not.
func TestAPerfectlyConsistentPressIsFlagged(t *testing.T) {
	const offset = 5
	actionableSince := -1

	parsed := botLog(t, 6000, func(st *sim.GameState, f int) [2]uint16 {
		me := &st.Players[0]
		if !sim.Actionable(me.State) {
			actionableSince = -1
			return [2]uint16{0, 0}
		}
		if actionableSince < 0 {
			actionableSince = f
		}
		if f-actionableSince == offset {
			actionableSince = -1
			return [2]uint16{sim.InHP, 0}
		}
		return [2]uint16{0, 0}
	})

	got := kinds(plausibility(parsed), 1)
	a, ok := got["consistency"]
	if !ok {
		t.Fatalf("a press at a fixed offset was not flagged: %+v", got)
	}
	if a.Value >= consistencyMax {
		t.Errorf("a fixed offset deviated by %v frames, above the threshold", a.Value)
	}
	if a.Sample < consistencySample {
		t.Errorf("flagged on %d observations", a.Sample)
	}
}

// The same bot with a human's scatter must clear the check, or the threshold is
// measuring "presses buttons" rather than "presses them mechanically".
func TestAScatteredPressIsNotFlagged(t *testing.T) {
	actionableSince := -1
	x := uint32(12345)
	scatter := func() int {
		x = x*1664525 + 1013904223
		return 3 + int(x>>28) // 3..18 frames, which is a human's spread
	}
	wait := scatter()

	parsed := botLog(t, 6000, func(st *sim.GameState, f int) [2]uint16 {
		me := &st.Players[0]
		if !sim.Actionable(me.State) {
			actionableSince = -1
			return [2]uint16{0, 0}
		}
		if actionableSince < 0 {
			actionableSince, wait = f, scatter()
		}
		if f-actionableSince == wait {
			actionableSince = -1
			return [2]uint16{sim.InHP, 0}
		}
		return [2]uint16{0, 0}
	})

	if a, ok := kinds(plausibility(parsed), 1)["consistency"]; ok {
		t.Errorf("a scattered press was flagged: %+v", a)
	}
}

// **The project's own scripted opponent, run through check 3.** It is a real
// bot rather than a stand-in for one: the same rule list a player faces in
// versus-AI, played as a device so its presses land in the log the way a
// cheating client's would (03 Game Design/AI Opponent.md).
//
// Whatever it reports is worth knowing. It is deliberately imperfect — a roll
// throws the rule list away some of the time, and reaction delay is one of the
// two difficulty levers — so an opponent tuned to be beatable is also one tuned
// to look human, which is the more interesting half of the result.
func TestWhatTheScriptedOpponentLooksLike(t *testing.T) {
	loadRoster()

	setup := sim.Setup{Chars: [2]int32{0, 1}}
	s := sim.NewSessionOf(setup)
	device := sim.NewAIDevice(sim.AIHard, 7)

	inputs := make([][2]uint16, 0, 6000)
	for range 6000 {
		in := [2]uint16{0, device.Press(s.State(), 1)}
		s.Advance(in)
		inputs = append(inputs, in)
	}

	parsed, err := replaylog.Decode("test", replaylog.Encode(setup, loadRoster(), inputs))
	if err != nil {
		t.Fatal(err)
	}

	found := kinds(plausibility(parsed), 2)
	t.Logf("the scripted opponent at Hard: %+v", found)

	// **A real bot is caught, and it is the macro check that catches it** —
	// the one needing no simulation and no threshold judgement. The two
	// probabilistic checks let it through, and that is not a failure of theirs:
	// reaction delay and decision randomness are its difficulty levers, so an
	// opponent tuned to be beatable is also one tuned to look human on exactly
	// the two axes they measure.
	//
	// Retuning the AI can legitimately move this. A failure here is a prompt to
	// look at what changed rather than a bug on its own.
	if _, ok := found["macro"]; !ok {
		t.Errorf("nothing caught the project's own bot: %+v", found)
	}

	// The one thing that would be a bug rather than a finding: the idle seat,
	// which pressed nothing at all, must never be flagged for it.
	if idle := kinds(plausibility(parsed), 1); len(idle) > 0 {
		t.Errorf("the seat that pressed nothing was flagged: %+v", idle)
	}
}
