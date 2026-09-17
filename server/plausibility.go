// Check 3: does the input stream look like it was produced by a person?
// (02 Architecture/Anti-Cheat and Verification.md.)
//
// **The only check that addresses a cheat playing by the rules.** Checks 1 and
// 2 catch a client that lied about the match; an assist bot does not lie — it
// plays a real match with inhuman execution, and every hash it reports is
// correct. Nothing here is proof of anything: it is **inherently probabilistic,
// it flags for review and it never punishes** (D53-style consequence, see the
// note's own table).
//
// False positives are real and are the point of saying so. A player who
// genuinely has fast reactions, a short match with a small sample, an unusual
// but legitimate habit — all of them can trip a threshold. Every check below
// therefore carries a minimum sample size, and the counts are stored rather
// than only the verdict, because **the signal is across matches rather than
// inside one of them**. One match saying "median reaction 11 frames over 12
// observations" is noise; the same account saying it forty times is not.
package main

import (
	"fmt"
	"math"
	"sort"

	"universus/sim"
	"universus/sim/replaylog"
)

// anomaly is one thing worth a person's attention, about one seat.
type anomaly struct {
	Kind string `json:"kind"`
	// 1 or 2 — the seat, resolved to an account by the caller.
	Seat int `json:"seat"`
	// What was measured and over how many observations. Both are reported
	// because a rate without its sample size is not a measurement.
	Value  float64 `json:"value"`
	Sample int     `json:"sample"`
	Note   string  `json:"note"`
}

// Thresholds. Every one of them is a judgement call rather than a derived
// bound, and they are here in one block so the thesis can quote them.
const (
	// **Reaction.** Human visual reaction to an unanticipated stimulus is
	// ~200 ms at the fast end, which is 12 frames at 60 Hz, and that is before
	// deciding what to do about it. A median *below* it across many separate
	// events is the shape of something reading state rather than a screen.
	reactionFloor = 12
	reactionMin   = 10 // observations before the median means anything

	// **Consistency.** A person's timing scatters. Pressing at the same offset
	// every time, over a large sample, is what a script looks like — and it is
	// the check that catches a bot which is deliberately *slow* enough to pass
	// the reaction floor.
	consistencyMax    = 0.75 // frames of standard deviation
	consistencySample = 20

	// **Macro.** A window of inputs repeated byte for byte. Twenty frames is a
	// third of a second of exact agreement, which a human hand does not
	// produce twice, let alone three times.
	macroWindow  = 20
	macroRepeats = 3
	// A window has to contain actual play: a held direction repeats forever
	// and means nothing.
	macroDistinct = 3
)

// The input bits this file reads. Mirrors sim/input.go, which is the same
// bitfield the sim, the network and the replay all see.
const (
	inLeft     = 1 << 2
	inRight    = 1 << 3
	attackBits = 0b111_0111_0000 // the six attack buttons, bits 4-6 and 8-10
)

// plausibility replays the log and reports what looks inhuman.
//
// It re-simulates rather than reading the inputs alone, because two of the
// three checks are about *responses to what was happening on screen*, and the
// only way to know what was happening is to play it again.
func plausibility(parsed replaylog.Log) []anomaly {
	var out []anomaly

	for seat := range 2 {
		out = append(out, macroAnomaly(parsed.Inputs, seat)...)
	}
	out = append(out, replayAnomalies(parsed)...)

	// Stable order, so two runs over the same match produce the same record.
	sort.Slice(out, func(i, j int) bool {
		if out[i].Seat != out[j].Seat {
			return out[i].Seat < out[j].Seat
		}
		return out[i].Kind < out[j].Kind
	})
	return out
}

// macroAnomaly looks for a window of inputs repeated byte for byte.
//
// **The one check that needs no simulation at all**: it is a property of the
// bitfields, which is also why it is the hardest to argue with. A human does
// not press the same twenty frames twice.
func macroAnomaly(inputs [][2]uint16, seat int) []anomaly {
	if len(inputs) < macroWindow*2 {
		return nil
	}

	counts := map[string]int{}
	best := 0
	for i := 0; i+macroWindow <= len(inputs); i++ {
		window := make([]byte, 0, macroWindow*2)
		distinct := map[uint16]bool{}
		attacked := false
		for f := range macroWindow {
			bits := inputs[i+f][seat]
			window = append(window, byte(bits), byte(bits>>8))
			distinct[bits] = true
			if bits&attackBits != 0 {
				attacked = true
			}
		}
		// A held direction repeats forever and says nothing; a window worth
		// counting has movement in it and at least one button.
		if len(distinct) < macroDistinct || !attacked {
			continue
		}
		key := string(window)
		counts[key]++
		if counts[key] > best {
			best = counts[key]
		}
	}

	if best < macroRepeats {
		return nil
	}
	return []anomaly{{
		Kind: "macro", Seat: seat + 1, Value: float64(best), Sample: len(inputs),
		Note: fmt.Sprintf("a %d-frame input sequence repeated byte for byte %d times",
			macroWindow, best),
	}}
}

// replayAnomalies plays the match again and measures what each seat did in
// response to what the other one was doing.
func replayAnomalies(parsed replaylog.Log) []anomaly {
	s := sim.NewSessionOf(parsed.Setup)

	var reactions [2][]int
	var offsets [2][]int
	// Frame the opponent's current attack started, and whether this seat has
	// already answered it. -1 for "nothing to react to".
	var pending [2]int
	var wasActionable, wasBlocking [2]bool
	var actionableSince [2]int
	pending = [2]int{-1, -1}
	actionableSince = [2]int{-1, -1}

	for f, in := range parsed.Inputs {
		before := *s.State()
		s.Advance(in)
		st := s.State()

		for seat := range 2 {
			me, them := &st.Players[seat], &st.Players[1-seat]
			blocking := holdingAway(in[seat], me.Facing)

			// --- reaction ---------------------------------------------------
			// The opponent started an attack this frame, and this seat was not
			// already holding away. Only then is what follows a *reaction*
			// rather than a stance somebody was already in.
			//
			// The transition is read by comparing the two frames rather than by
			// asking for StateFrame 0: what a caller can observe depends on
			// whether it looks before or after the advance, and a check that
			// depends on that is one that silently observes nothing.
			started := before.Players[1-seat].State != sim.StateAttack &&
				them.State == sim.StateAttack
			if started && sim.Actionable(me.State) && !wasBlocking[seat] {
				pending[seat] = f
			}
			if pending[seat] >= 0 && blocking {
				reactions[seat] = append(reactions[seat], f-pending[seat])
				pending[seat] = -1
			}
			// An attack that ended without an answer is not an observation: a
			// player who never blocked it did not react slowly, they chose
			// something else.
			if pending[seat] >= 0 && them.State != sim.StateAttack {
				pending[seat] = -1
			}

			// --- consistency ------------------------------------------------
			// How long after becoming able to act the next button arrives. A
			// person scatters; a script does not.
			actionable := sim.Actionable(me.State)
			if actionable && !wasActionable[seat] {
				actionableSince[seat] = f
			}
			if actionableSince[seat] >= 0 && in[seat]&attackBits != 0 &&
				before.Players[seat].MoveIndex < 0 {
				offsets[seat] = append(offsets[seat], f-actionableSince[seat])
				actionableSince[seat] = -1
			}
			if !actionable {
				actionableSince[seat] = -1
			}

			wasActionable[seat] = actionable
			wasBlocking[seat] = blocking
		}
	}

	var out []anomaly
	for seat := range 2 {
		if r := reactions[seat]; len(r) >= reactionMin {
			if med := median(r); med < reactionFloor {
				out = append(out, anomaly{
					Kind: "reaction", Seat: seat + 1, Value: med, Sample: len(r),
					Note: fmt.Sprintf("median %.1f frames to block an attack that started, "+
						"against a human floor of %d", med, reactionFloor),
				})
			}
		}
		if o := offsets[seat]; len(o) >= consistencySample {
			if sd := stddev(o); sd < consistencyMax {
				out = append(out, anomaly{
					Kind: "consistency", Seat: seat + 1, Value: sd, Sample: len(o),
					Note: fmt.Sprintf("presses land %.2f frames of standard deviation after "+
						"becoming actionable; a hand scatters more than that", sd),
				})
			}
		}
	}
	return out
}

// holdingAway is what blocking is: there is no block button, so the input and
// the facing are the whole of it.
func holdingAway(bits uint16, facing int32) bool {
	if facing > 0 {
		return bits&inLeft != 0
	}
	return bits&inRight != 0
}

func median(xs []int) float64 {
	sorted := append([]int(nil), xs...)
	sort.Ints(sorted)
	mid := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return float64(sorted[mid])
	}
	return float64(sorted[mid-1]+sorted[mid]) / 2
}

func stddev(xs []int) float64 {
	if len(xs) < 2 {
		return math.Inf(1)
	}
	mean := 0.0
	for _, x := range xs {
		mean += float64(x)
	}
	mean /= float64(len(xs))

	sum := 0.0
	for _, x := range xs {
		d := float64(x) - mean
		sum += d * d
	}
	return math.Sqrt(sum / float64(len(xs)-1))
}
