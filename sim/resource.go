package sim

// The two resources, per 03 Game Design/Resource System.md. Drive is a
// per-round tactical gauge — full at the start, spent by defence, and the
// system that gives the game its texture. Super is a per-match strategic one:
// built by fighting, never regenerating, carried between rounds.
//
// Both live in GameState, so both roll back. A meter that does not roll back
// is a super that is available on one machine and not the other.

// Units. **One bar is 1000 units, and a resource is never a float or a fraction
// of a bar** — the design note is explicit, because the 0.5-bar Drive Parry has
// to be an exact integer cost and half of an odd number is not.
const (
	BarUnits = 1000

	DriveBars = 6
	SuperBars = 3

	DriveMax = DriveBars * BarUnits
	SuperMax = SuperBars * BarUnits
)

// Balance is the global tunables: costs, regeneration rates and the Burnout
// penalties. Not character data — it is one set of numbers for the match — but
// it is loaded the same way and for the same reason. Two clients with different
// Drive costs run different simulations from identical inputs.
//
// Every value is a plain integer. Rates are units per frame; percentages are
// out of 100 and are applied as a multiply followed by a divide, never the
// other way round.
type Balance struct {
	// DriveRegen is the neutral rate, DriveRegenWalkF the faster one for
	// walking forward — moving in is rewarded, and that asymmetry is the whole
	// point of having two rates. Regeneration pauses in blockstun.
	DriveRegen      int32
	DriveRegenWalkF int32

	// DriveRegenBurnout is the refill rate while burnt out, which is what sets
	// how long Burnout lasts: DriveMax divided by this, in frames.
	DriveRegenBurnout int32

	// DriveBlockCost is spent per blocked hit. **Blocking spends Drive** — this
	// is the pressure loop, and the reason defence has a resource cost.
	DriveBlockCost int32

	// BurnoutBlockstun is the extra blockstun a burnt-out defender takes, and
	// BurnoutChipPercent the chip damage a blocked *special* deals to them —
	// the only situation in the game where blocking deals damage (D32).
	BurnoutBlockstun   int32
	BurnoutChipPercent int32

	// The damage pipeline (see damage.go). ComboScale is indexed by hit number
	// and its last entry is the floor; the starters are the multiplier the move
	// that began a combo applies to the whole of it.
	ComboScale       [ComboScaleSteps]int32
	StarterLight     int32
	StarterMedium    int32
	StarterHeavy     int32
	MinDamagePercent int32

	// A counter hit pays more damage and more hitstun; a punish counter — a hit
	// landed in the opponent's recovery — pays the same damage and more frames
	// still.
	CounterHitPercent int32
	CounterHitstun    int32
	PunishHitstun     int32

	// Round flow (round.go). Frame counts, not seconds: the sim has no clock
	// but the frame number, so the loader is what turns 99 seconds into 5940
	// frames. RoundsToWin is best-of-three's 2; MaxRounds is the cap that stops
	// draws extending the match forever (D54).
	RoundFrames  int32
	RoundsToWin  int32
	MaxRounds    int32
	KOFreeze     int32
	RoundEndHold int32
	IntroFrames  int32

	// Throws (03 Game Design/Movement and Defense.md). ThrowTechFrames is how
	// long the escape window is, ThrowTechRecovery what both players owe after
	// one, and ThrowTechPush how far apart they end up, in whole units — a
	// distance, not a speed, because a tech separates rather than launches.
	ThrowTechFrames   int32
	ThrowTechRecovery int32
	ThrowTechPush     int32

	// Super is built by dealing damage, by taking it, and by landing a special.
	// The first two are percentages of the damage; the third is flat, because
	// it is paid for the connect rather than for the numbers behind it.
	SuperDealtPercent int32
	SuperTakenPercent int32
	SuperOnSpecial    int32
}

// The loaded balance. Package-level and mutable-once, exactly like the roster
// and for the same reason: it is not gameplay state, it must not roll back, and
// two clients holding different values desync with no visible cause.
//
// The zero value is deliberate. **Nothing here is defaulted in code** — an
// unloaded balance is a game with no regeneration and no costs, which is
// obviously broken, rather than one quietly running numbers that are not in the
// JSON the data hash covers.
var balance Balance

// LoadBalance installs the tunables. Validation lives in package data with the
// rest of it, where the field names to complain about are.
func LoadBalance(b Balance) { balance = b }

// BalanceOf returns the loaded tunables. For tests and tools; the sim reads the
// package var directly.
func BalanceOf() Balance { return balance }

// updateResources is the resource half of step 8. Drive only: Super never
// regenerates, which is what makes it strategic.
func (s *GameState) updateResources() {
	for i := range s.Players {
		p := &s.Players[i]
		p.Drive += p.driveRegen()
		if p.Drive >= DriveMax {
			p.Drive = DriveMax
			// Burnout ends when the gauge is *full*, not when it is above zero.
			// Ending it early would hand back the Drive mechanics for one bar
			// and make being burnt out a formality.
			p.Burnout = 0
		}
	}
}

// driveRegen is this frame's regeneration rate.
func (p *PlayerState) driveRegen() int32 {
	switch {
	case p.Burnout != 0:
		return balance.DriveRegenBurnout
	case p.State == StateBlockstun:
		// Paused while blocking. Otherwise the gauge the attacker is draining
		// refills between their hits and the pressure loop does not close.
		return 0
	case p.State == StateWalkF:
		return balance.DriveRegenWalkF
	default:
		return balance.DriveRegen
	}
}

// spendDrive takes n units and enters Burnout if that empties the gauge.
//
// It cannot fail: everything that spends Drive today is blocking, which is not
// optional. A mechanic the player chooses — Impact, Rush, EX — checks the
// gauge before it comes out, and that check belongs with the mechanic.
func (p *PlayerState) spendDrive(n int32) {
	p.Drive -= n
	if p.Drive <= 0 {
		p.Drive = 0
		p.Burnout = 1
	}
}

// gainSuper adds n units, capped. Called for both fighters on a connect: the
// one dealing damage and the one taking it both build meter.
func (p *PlayerState) gainSuper(n int32) {
	p.Super += n
	if p.Super > SuperMax {
		p.Super = SuperMax
	}
}
