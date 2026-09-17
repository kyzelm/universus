package main

import "testing"

func TestAnEvenMatchIsWorthAboutThirty(t *testing.T) {
	if got := lpChange(1000, 1000); got != 30 {
		t.Errorf("an even match paid %d, want %d", got, lpBase)
	}
}

// The sentence the design note actually writes down: more for beating somebody
// higher rated, less for beating somebody lower.
func TestBeatingSomebodyBetterPaysMore(t *testing.T) {
	even := lpChange(1000, 1000)
	underdog := lpChange(1000, 1400)
	favourite := lpChange(1400, 1000)

	if !(favourite < even && even < underdog) {
		t.Errorf("favourite %d, even %d, underdog %d: want them in that order",
			favourite, even, underdog)
	}
}

// A mismatch must not pay a tier in one match, in either direction.
func TestThePayoutIsBounded(t *testing.T) {
	for _, c := range [][2]int{{0, 99999}, {99999, 0}, {0, 0}, {12000, 11999}} {
		got := lpChange(c[0], c[1])
		if got < lpMin || got > lpMax {
			t.Errorf("lpChange%v = %d, outside %d..%d", c, got, lpMin, lpMax)
		}
	}
}

func TestTiersAreTheTableFromTheDesign(t *testing.T) {
	for _, c := range []struct {
		lp   int
		tier int
	}{
		{0, 0}, {999, 0}, {1000, 1}, {2499, 1}, {2500, 2},
		{5000, 3}, {8000, 4}, {12000, 5}, {999999, 5},
	} {
		if got := tierOf(c.lp); got != c.tier {
			t.Errorf("%d LP is tier %d (%s), want %d (%s)",
				c.lp, got, tierNames[got], c.tier, tierNames[c.tier])
		}
	}
}

// **A player cannot demote out of a tier.** Standard, kind, and it removes a
// whole class of complaints.
func TestALossNeverLeavesTheTier(t *testing.T) {
	// One point into Sophomore, losing far more than that.
	if got := applyLoss(1005, 50); got != 1000 {
		t.Errorf("1005 LP lost 50 and landed on %d, want the 1000 floor", got)
	}
	// Well inside a tier, the loss is ordinary.
	if got := applyLoss(2000, 30); got != 1970 {
		t.Errorf("2000 LP lost 30 and landed on %d", got)
	}
	// The bottom of the ladder cannot go negative.
	if got := applyLoss(5, 30); got != 0 {
		t.Errorf("5 LP lost 30 and landed on %d, want 0", got)
	}
	// And the floor is the tier held *before* the loss, not after it — the
	// circular reading would let a player fall to the previous tier's floor.
	if got := applyLoss(1000, 50); got != 1000 {
		t.Errorf("exactly on a boundary, a loss took it to %d", got)
	}
}
