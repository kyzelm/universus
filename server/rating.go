package main

// Ladder points and tiers (03 Game Design/Game Modes.md, D44).
//
// All integer, and not because of the determinism rule — that binds the sim and
// nothing out here. It is because LP is a number players read, compare and
// argue about, and a rating that depends on floating-point rounding is one
// where the same match played twice can pay differently.

// Tier floors, in LP. Index is the tier stored in `ratings.tier`.
var tierFloors = []int{0, 1000, 2500, 5000, 8000, 12000}

// TierNames are the academic ranks the world uses (04 Art/World and Theme.md).
var tierNames = []string{"Freshman", "Sophomore", "Junior", "Senior", "Graduate", "Master"}

// tierOf is the tier a rating sits in.
func tierOf(lp int) int {
	t := 0
	for i, floor := range tierFloors {
		if lp >= floor {
			t = i
		}
	}
	return t
}

// The rating change for a win, in LP.
//
// **Roughly +30 for an even match, more for beating somebody higher rated, less
// for beating somebody lower** — the design note's own words, as the smallest
// arithmetic that produces them. Not Elo: Elo's expected-score curve is a
// logistic function whose only visible effect at this scale is to make the same
// sentence harder to explain and impossible to compute in integers.
//
// The gap is divided *after* the multiplication, and the bounds are what stop a
// wildly mismatched pair from paying a whole tier in one match.
const (
	lpBase = 30
	lpMin  = 10
	lpMax  = 50
	// LP of gap that moves the payout by one point. 40 makes a 400-point
	// underdog win worth +40 and a 400-point favourite's win worth +20.
	lpGapPerPoint = 40
)

func lpChange(winnerLP, loserLP int) int {
	change := lpBase + (loserLP-winnerLP)/lpGapPerPoint
	return min(max(change, lpMin), lpMax)
}

// applyLoss takes the points off, but never out of the tier the player was in.
//
// **The floor is standard, it is kind, and it removes a whole class of
// complaints**: a player who reaches Junior stays Junior. It is computed from
// the tier held *before* the loss, because the tier after it is the thing being
// constrained and using it would be circular.
func applyLoss(lp, change int) int {
	floor := tierFloors[tierOf(lp)]
	return max(lp-change, floor)
}
