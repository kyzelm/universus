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

	// DriveParryDrain is what the parry stance costs per frame it is held, and
	// DriveParryGain what a successful absorb hands back.
	//
	// The design writes the cost as "0.5 bar, held" (03 Game Design/Resource
	// System.md). A per-frame drain is the reading that makes it a cost of
	// *holding*: a flat charge on the press would make a parry held all round
	// cost the same as one held for three frames, which is the stance nobody
	// would ever leave. Authored so that the half bar buys about the length of
	// a real parry attempt.
	DriveParryDrain int32
	DriveParryGain  int32

	// Drive Rush: what it costs from the parry stance, what it costs as a
	// cancel out of a connected normal, how fast it travels and for how long.
	// The two prices are the design's own (1 bar and 3), and they are apart
	// because they buy different things — approach against a combo.
	DriveRushCost       int32
	DriveRushCancelCost int32
	DriveRushSpeed      Fix
	DriveRushFrames     int32

	// ArmorDamagePercent is how much of a move's damage an absorbed hit still
	// deals to an armoured defender (see Move.Armor). Reduced rather than
	// nothing, so armouring through a heavy is a decision with a price; it
	// cannot kill, which is enforced where it is applied.
	ArmorDamagePercent int32

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

	// Knockback (knockback.go). KnockbackHit and KnockbackBlock are the default
	// push a connect gives the defender, in units per frame; a move that wants
	// its own — a launcher — authors it. **The block value is the larger one**:
	// it is what spaces a blockstring out and therefore what decides whether
	// pressure continues. KnockbackDecay is the percentage of the push kept per
	// grounded frame, which is the friction that ends the slide.
	KnockbackHit   Fix
	KnockbackBlock Fix
	KnockbackDecay int32

	// Juggles. JuggleLimit is how many hits an airborne defender takes before
	// moves stop connecting, standing in for every move that does not name its
	// own; JuggleGravityPercent is how much heavier each juggle hit makes them
	// fall, which is what makes an air combo self-terminate rather than merely
	// hit a cap.
	JuggleLimit          int32
	JuggleGravityPercent int32

	// KnockdownFrames is how long a knocked-down player is on the ground, and
	// it is deliberately one number: the design note says wakeup timing stays
	// identical for every knockdown type, because varying it is a balance
	// rabbit hole that adds nothing the thesis needs.
	KnockdownFrames int32

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
	// Training mode keeps both fighters topped up — infinite resources, per the
	// mode's own list. Not while anybody is being hit: a gauge that refilled
	// through a combo would hide the damage the combo did, which is the one
	// thing the person practising is looking at.
	if s.Training != 0 && !s.anyoneStunned() {
		for i := range s.Players {
			p := &s.Players[i]
			p.Health = CharacterAt(p.Char).Health
			p.Drive, p.Super = DriveMax, SuperMax
			p.Burnout = 0
		}
		return
	}

	for i := range s.Players {
		p := &s.Players[i]

		// The parry drains rather than regenerates, and running the gauge out
		// with it is Burnout like any other way of reaching zero — which is
		// what stops the stance being free to sit in.
		if p.State == StateParry {
			p.spendDrive(balance.DriveParryDrain)
			continue
		}

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

// anyoneStunned reports whether either fighter is mid-consequence: hitstun,
// blockstun, a knockdown or a throw. Training's refill waits for it to end.
func (s *GameState) anyoneStunned() bool {
	return stunned(s.Players[0].State) || stunned(s.Players[1].State) ||
		s.Players[0].Combo > 0 || s.Players[1].Combo > 0
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
// It cannot fail: blocking is not optional, and a mechanic the player chooses
// has already been refused at selection if the gauge could not pay for it
// (moveFor). So this is a deduction, never a check — the one place that can
// take Drive, which is what keeps "it came out" and "it was paid for" from
// disagreeing.
func (p *PlayerState) spendDrive(n int32) {
	p.Drive -= n
	if p.Drive <= 0 {
		p.Drive = 0
		p.Burnout = 1
	}
}

// gainDrive hands n units back, capped. Burnout is deliberately not cleared
// here: it ends when the gauge is *full* and nowhere else (see
// updateResources), so a parry landed while burnt out — which cannot happen —
// would not shorten it either.
func (p *PlayerState) gainDrive(n int32) {
	p.Drive += n
	if p.Drive > DriveMax {
		p.Drive = DriveMax
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
