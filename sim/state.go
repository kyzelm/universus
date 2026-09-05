package sim

// Input bitfield — one uint16 per player per frame. The same bytes go to the
// sim, the network, the replay and the server. Bits 7, 11-15 are reserved.
// SOCD is resolved client-side before packing; the sim trusts what it gets.
const (
	InUp    uint16 = 1 << 0
	InDown  uint16 = 1 << 1
	InLeft  uint16 = 1 << 2
	InRight uint16 = 1 << 3
	InLP    uint16 = 1 << 4
	InMP    uint16 = 1 << 5
	InHP    uint16 = 1 << 6
	InLK    uint16 = 1 << 8
	InMK    uint16 = 1 << 9
	InHK    uint16 = 1 << 10
)

// Stage geometry. Not character data — it belongs to the stage, and there is
// one stage.
const (
	GroundY        = Fix(0)
	StageHalfWidth = Fix(200) << FracBits // ~400 units wide

	// Half the view width in game units. The camera keeps this much visible on
	// each side of its centre and never shows past a wall.
	CameraHalfWidth = Fix(200) << FracBits
)

// PlayerState is one fighter. Every field is 4 bytes — see the note on
// GameState.
type PlayerState struct {
	X, Y   Fix
	VX, VY Fix

	// Facing is +1 (right) or -1 (left). Every motion is read relative to it —
	// quarter-circle forward is ↓↘→ facing right and ↓↙← facing left, and the
	// player pressed the same thing both times. Maintained by updateFacing.
	Facing int32

	// Char indexes the loaded roster.
	Char int32

	Health int32

	// Drive and Super are the two resources (03 Game Design/Resource System.md),
	// in units of a thousandth of a bar — see resource.go. Drive starts full
	// every round and is spent by defence; Super starts empty, is built by
	// fighting, and carries between rounds.
	Drive int32
	Super int32

	// Combo is how many hits the player has taken without recovering, and
	// ComboStarter the scaling multiplier captured from the move that began it
	// — captured, because the starter scales the whole combo and the move that
	// started it is gone by the third hit.
	//
	// Counter is the class of the most recent hit taken (CounterNone,
	// CounterHit, CounterPunish). In the state because the HUD reads it: a
	// counter hit nobody can see is one nobody learns from.
	Combo        int32
	ComboStarter int32
	Counter      int32

	// Burnout is 1 while the Drive gauge is refilling from empty. A modifier
	// flag, not a state: a burnt-out player still walks, attacks and blocks —
	// they do it with longer blockstun, chip damage on blocked specials, and no
	// Drive mechanics at all.
	Burnout int32

	// State and StateFrame are the state machine. StateFrame counts from 0 on
	// the frame the state was entered.
	State      int32
	StateFrame int32

	// MoveIndex is the active move while State is StateAttack, else -1.
	MoveIndex int32

	// HasHit marks a move that has already connected, so one active window
	// cannot hit twice. Cleared when the move starts.
	HasHit int32

	// Stun is the remaining hitstun, blockstun or landing recovery. One
	// counter, because all three are the same thing to the state machine: a
	// fixed number of frames that accept no input.
	Stun int32

	// Landing is the recovery this player owes the moment they touch down —
	// the debt of a move that ended while they were still in the air, carried
	// because the move itself is gone by then. Cleared by every state change,
	// so being hit out of the fall cancels it.
	Landing int32

	// Down is 1 while this player owes a knockdown at the end of their current
	// stun. The same debt shape as Landing and for the same reason: the move
	// that knocked them down is gone by the time its hitstun runs out, and
	// nothing else remembers that this hitstun ends on the floor.
	//
	// Cleared by every state change, so being hit out of the hitstun replaces
	// the old hit's consequences with the new one's rather than stacking them.
	Down int32

	// Eaten is the newest frame whose button presses have already been spent,
	// or -1. The input buffer keeps a press live for a few frames looking for a
	// state that can act on it (see moveFor); without a record of which presses
	// were already spent, the same tap keeps matching for the rest of the
	// window and one press produces a move on every actionable frame.
	//
	// In the state, not beside it, for the usual reason: a buffer that does not
	// roll back replays the wrong move.
	Eaten int32

	// JumpVX is the horizontal velocity committed at pre-jump. There is no air
	// control, so the whole arc follows from this and gravity.
	JumpVX Fix

	// Inputs is the input history ring: Inputs[f%InputHistory] is the bitfield
	// that advanced frame f, widened to uint32 to keep GameState padding-free.
	//
	// In the state, not beside it, because motion recognition reads it: history
	// outside GameState means a rollback replays a fireball as a crouch.
	Inputs [InputHistory]uint32
}

// GameState is all gameplay state. Flat, fixed-size, comparable: no pointers,
// no slices, no maps. Every field is 4-byte, so the struct has no implicit
// padding and no uninitialised bytes to leak into a checksum.
//
// **Every field must stay 4 bytes wide and 4-byte aligned.** That is what keeps
// the struct padding-free, which is what makes hashing its raw memory sound —
// see Checksum. A narrower field costs less space than the padding it forces,
// and TestGameStateHasNoImplicitPadding fails the moment one is added.
//
// If it is not in here, it does not roll back, and it is a bug. Camera, RNG,
// input history, AI state, timers and round transitions all belong here.
type GameState struct {
	Frame uint32
	// RNG is the whole generator — see rand.go. It rolls back because it is a
	// field, and for no other reason.
	RNG uint32

	// Hitstop freezes both fighters for a few frames on a connect. It is not
	// cosmetic: it changes when the next action can happen, so it is in the
	// sim and it is in the state.
	Hitstop int32

	// Round flow — see round.go. Phase is what the match is doing (fighting,
	// the KO freeze, the result, the next round's intro) and PhaseFrame is its
	// clock. In the state because they decide when input resumes, which is
	// gameplay and not presentation.
	Phase      int32
	PhaseFrame int32

	// Timer is the round clock, counted in frames because the sim has no other
	// clock and is never allowed one.
	Timer int32

	// Round is the 1-based number of the round being played and Wins the rounds
	// each player has taken. A draw awards neither of them (D53).
	Round int32
	Wins  [2]int32

	// RoundWinner is who took the round just decided, RoundNobody for a draw;
	// Winner is the match winner once Phase is PhaseMatchEnd and RoundNobody
	// until then. Both are here rather than derived because the view draws them
	// and a rewind has to take them back.
	RoundWinner int32
	Winner      int32

	// Dealt is cumulative damage dealt across the whole match, per player. It
	// exists for one thing: the tiebreak when the round cap runs out (D54). It
	// therefore survives a round reset, which is why it is here and not on the
	// player.
	Dealt [2]int32

	// CamX is the camera centre. **In the state, because corner position is
	// gameplay** — a desynced camera is a desynced corner, and the corner is
	// where a large part of the game happens.
	CamX Fix

	Players [2]PlayerState

	// Projectiles is the pool. In the state for the same reason as everything
	// else in here: it rolls back, or a fireball that was rewound keeps flying
	// on one machine only.
	Projectiles [MaxProjectiles]Projectile
}

// New returns the starting state with both players on character 0.
func New() GameState { return NewMatch(0, 0) }

// NewMatch starts a match between two roster entries: players apart, on the
// ground, facing each other, at full health.
func NewMatch(c0, c1 int32) GameState {
	s := GameState{RNG: seed, Players: [2]PlayerState{
		{X: Fix(-60) << FracBits, Facing: 1, Char: c0, Health: CharacterAt(c0).Health},
		{X: Fix(60) << FracBits, Facing: -1, Char: c1, Health: CharacterAt(c1).Health},
	}}
	for i := range s.Players {
		s.Players[i].MoveIndex = -1
		s.Players[i].Eaten = -1
		// Drive starts full and Super starts empty. Super is the one thing that
		// carries between rounds (see startRound), which is what makes it the
		// strategic gauge of the two.
		s.Players[i].Drive = DriveMax
	}

	// Round 1, fighting. **A match starts in the fight, not in an intro**: the
	// intro is the gap between rounds, and one in front of frame 0 would mean
	// every replay, every test and every determinism run began with ninety
	// frames of nothing.
	s.Round = 1
	s.Timer = balance.RoundFrames
	s.RoundWinner = RoundNobody
	s.Winner = RoundNobody
	s.updateCamera()
	return s
}

// Advance runs one frame.
//
// The nine steps below are the update order, and the order is itself part of
// the spec — 02 Architecture/Deterministic Simulation.md. It is fixed, it is
// documented, and it is never reordered casually: two machines that run these
// steps in different orders produce different states from identical inputs,
// which is a desync with no other symptom.
func (s *GameState) Advance(in [2]uint16) {
	// 1. Resolve inputs. Record history first: motion recognition, the dash
	// double-tap and the input buffer all read the ring, and all must see this
	// frame.
	for i := range s.Players {
		s.Players[i].Inputs[s.Frame%InputHistory] = uint32(in[i])
	}

	// Hitstop freezes the whole match: no input, no movement, no new hits.
	// Only the counter runs, and the frame still advances so the checksum and
	// the network keep moving.
	if s.Hitstop > 0 {
		s.Hitstop--
		s.Frame++
		return
	}

	// Between rounds nothing moves. The KO freeze, the result and the intro run
	// their own clock and nothing else — no input, no movement, no hits — and
	// the frame still advances, so the checksum and the network keep going
	// through a round transition exactly as they do through hitstop.
	if s.Phase != PhaseFight {
		s.advancePhase()
		s.Frame++
		return
	}

	for i := range s.Players {
		s.resolveInputs(i, in[i])
	}

	// 2. Advance state machines.
	for i := range s.Players {
		s.advanceState(i)
	}

	// 3. Movement and gravity. Semi-implicit Euler: velocity first, then
	// position, so a landing is detected on the frame it happens.
	for i := range s.Players {
		p := &s.Players[i]

		// Whether the player was in the air is decided before the position
		// moves, and both the gravity and the landing below read that one
		// answer. Asking again afterwards would ask about a different frame.
		air := p.Airborne()
		if air {
			p.VY += CharacterAt(p.Char).Gravity
		}
		p.X += p.VX
		p.Y += p.VY

		if p.Y <= GroundY {
			p.Y = GroundY
			p.VY = 0
			if air {
				// Landing kills horizontal momentum, so a move that came down
				// does not slide through its recovery. Only for something that
				// was actually airborne: a grounded advancing move is on the
				// ground every frame and must keep the velocity its data gave
				// it.
				p.VX = 0

				// Landing ends a jump, and it ends an air normal with it: an
				// air move's recovery is the fall, and it has no business
				// continuing on the ground. A move that *launched* from the
				// ground — an uppercut — keeps its recovery, because those
				// frames are the punish window that makes it a risk.
				//
				// Either way the character owes what the move that put them up
				// there said they owe. Zero for a plain jump; without a value
				// for the rest, a whiffed uppercut that expires overhead and an
				// air normal held to the floor are both actionable on the frame
				// they touch it, which is a free reversal and a free jump-in.
				mv := p.move()
				switch {
				case mv != nil && mv.Stance == StanceAir:
					p.land(mv.Landing)
				case p.State == StateAir:
					p.land(p.Landing)
				}
			}
		}
	}

	// 4. Pushboxes and stage bounds, then turn to face the opponent.
	s.separate()
	s.clampToStage()
	s.updateFacing()

	// 5. Projectiles: they move before hit detection reads their boxes, and
	// after the players have been separated and clamped, so a fireball is
	// tested against final positions like everything else.
	s.advanceProjectiles()

	// 6. Hit detection, then 7. hit resolution. **Player 1's hitboxes against
	// player 2's hurtboxes first, then the reverse.** Both are collected before
	// either is applied, so a trade resolves identically on both machines
	// rather than depending on which player the loop reached first.
	//
	// The moves are captured alongside the hits, and for the same reason:
	// resolving player 0's hit puts player 1 in hitstun, which ends player 1's
	// attack — so by the time the second resolution ran, the move that was
	// about to land no longer existed and the trade silently became a
	// one-sided hit. Character data is immutable, so these pointers stay valid.
	mv0, mv1 := s.Players[0].move(), s.Players[1].move()
	hit0 := s.connects(0, 1)
	hit1 := s.connects(1, 0)
	if hit0 {
		s.resolveHit(0, 1, mv0, in[1])
	}
	if hit1 {
		s.resolveHit(1, 0, mv1, in[0])
	}

	// Projectiles resolve after both players, so a fireball and the punch that
	// beat it to the same frame trade in a fixed order rather than in whichever
	// order the loop happened to reach them.
	s.resolveProjectiles(in)

	// 8. Timers, resources, round state. The camera is here because it is
	// derived from positions, which are final by now, and the resources after
	// the hits that spent and built them.
	s.updateResources()

	// A combo lasts exactly as long as the hitstun holding it together. The
	// moment the defender is out of hitstun the next hit starts a new combo at
	// full damage, which is the definition of the combo ending — and this runs
	// after resolution, so a hit landed this frame is already counted.
	for i := range s.Players {
		if p := &s.Players[i]; p.State != StateHitstun {
			p.Combo = 0
			p.ComboStarter = 0
		}
	}
	s.updateCamera()

	// The round is resolved last of all, after the damage that might have ended
	// it and after the combo bookkeeping: a KO is a fact about the health bar
	// this frame left behind, not about the hit that was being applied.
	s.updateRound()

	// 9. Increment frame. The caller takes the checksum; the sim does not
	// store it, because a stored checksum would be state covering itself.
	s.Frame++
}

// connects reports whether attacker's hitboxes overlap defender's hurtboxes.
//
// Detection is separated from resolution so that both directions are tested
// against the same positions. Resolving as we go would let player 0's hit push
// player 1 out of range before player 1's hit is tested, and the trade would
// stop being a trade.
func (s *GameState) connects(attacker, defender int) bool {
	// A throw refuses a defender it is not allowed to take, and it refuses them
	// *here* rather than at resolution: a throw that whiffs has to actually
	// whiff. Its recovery is the price of trying, and a throw that quietly
	// counted as a connect would keep its cancel window and its once-per-move
	// hit flag while doing nothing at all.
	if mv := s.Players[attacker].move(); mv != nil && mv.IsThrow() && !s.throwable(defender) {
		return false
	}

	var hits, hurts [MaxBoxes]Box
	nh := s.Hitboxes(attacker, &hits)
	if nh == 0 {
		return false
	}
	nd := s.Hurtboxes(defender, &hurts)

	for a := int32(0); a < nh; a++ {
		for d := int32(0); d < nd; d++ {
			if hits[a].Overlaps(hurts[d]) {
				return true
			}
		}
	}
	return false
}

// resolveHit applies one connect. mv is the attacker's move as captured before
// any resolution ran; defenderIn is the defender's input this frame, which is
// what decides whether they were holding back.
func (s *GameState) resolveHit(attacker, defender int, mv *Move, defenderIn uint16) {
	ap := &s.Players[attacker]

	if mv == nil {
		return
	}
	// A throw is resolved whole, by its own path: it cannot be blocked, cannot
	// counter-hit, and may be escaped by a tech that ends the attacker's move
	// too — which is why it marks the connect itself rather than being marked
	// here and then possibly undone.
	if mv.IsThrow() {
		s.applyThrow(attacker, defender, mv, s.Frame)
		return
	}

	ap.HasHit = 1
	s.applyHit(attacker, defender, mv, defenderIn)
}

// applyHit is the half of a connect that lands on the defender: block or hit,
// stun, damage, resources, hitstop. Split out because a projectile connects
// without any attacker to *mark* — the fireball's owner may have recovered and
// walked away frames ago, and marking their current move as having hit would
// disable a hitbox they are holding out right now. They still own the meter it
// builds, which is why the index comes along.
func (s *GameState) applyHit(attacker, defender int, mv *Move, defenderIn uint16) {
	ap, dp := &s.Players[attacker], &s.Players[defender]

	// Damage dealt is measured off the health bar at the end of this function
	// rather than taken from the formula, so the match total the round cap's
	// tiebreak reads is what actually happened: chip that stopped at 1 health
	// counts what it removed, and a hit that overkills counts what was left.
	before := dp.Health

	if s.blocking(defender, defenderIn, mv.Level) {
		// A blocked hit is not a hit: it ends no combo and starts none, and the
		// counter class of the last real hit stands until the next one.
		dp.enter(StateBlockstun)
		dp.Stun = mv.Blockstun

		if dp.Burnout != 0 {
			// The Burnout penalties, and the only place in the game where
			// blocking deals damage (D32). No further Drive is taken: the
			// gauge is already empty and refilling, and taking from it again
			// would extend Burnout for as long as the pressure lasts.
			dp.Stun += balance.BurnoutBlockstun
			if mv.Motion != MotionNone {
				// Chip, and chip cannot kill. Losing a round to a blocked
				// fireball is the most unsatisfying way to lose one.
				dp.chip(mv.Damage * balance.BurnoutChipPercent / 100)
			}
		} else {
			// **Blocking spends Drive.** This is the pressure loop: defence
			// costs a resource, and the resource running out is Burnout.
			dp.spendDrive(balance.DriveBlockCost)
		}
	} else {
		// The counter class is read before anything is applied: it is a fact
		// about the defender's own attack, and entering hitstun destroys it.
		counter := s.counterClass(defender)

		// The starter scales the whole combo, so it is captured on the hit that
		// begins one and never recomputed. Reading it off the current move
		// instead would let a combo that started with a jab finish at heavy
		// scaling, which is the entire thing starter scaling exists to stop.
		if dp.Combo == 0 {
			dp.ComboStarter = starterScale(mv)
		}
		dp.Combo++
		dp.Counter = counter

		dp.enter(StateHitstun)
		dp.Stun = mv.Hitstun + counterHitstun(counter)

		// Owed after the hitstun, not instead of it, and set after enter
		// because entering a state is what clears the previous debt.
		if mv.KnocksDown() {
			dp.Down = 1
		}

		dp.hurt(scaledDamage(mv, dp.ComboStarter, dp.Combo, counter))

		// Super is built from the move's base damage, not the scaled figure.
		// Metering off the scaled number would pay less for the tenth hit of a
		// combo than for the first, which quietly makes long combos worse than
		// short ones at building meter — a balance decision nobody made.
		ap.gainSuper(mv.Damage * balance.SuperDealtPercent / 100)
		dp.gainSuper(mv.Damage * balance.SuperTakenPercent / 100)

		// ponytail: no juggles. A juggle counter needs something that puts the
		// defender in the air, and no move launches anyone but its own owner —
		// juggles arrive with knockdowns and launchers, not before.
	}

	// A special that connects pays a flat bonus whether it hit or was blocked.
	// It is paid for the connect, not for the damage behind it.
	if mv.Motion != MotionNone {
		ap.gainSuper(balance.SuperOnSpecial)
	}

	// Hitstop is one value for the match, not one per player: both fighters
	// freeze together, which is the whole effect. A trade takes the larger.
	if mv.Hitstop > s.Hitstop {
		s.Hitstop = mv.Hitstop
	}

	// One place, so every source of damage — hits, chip, projectiles, whatever
	// comes next — is counted without anyone having to remember to.
	s.Dealt[attacker] += before - dp.Health
}

// separate pushes overlapping pushboxes apart, half the overlap each, then the
// stage clamp runs after. A player pinned against a wall would otherwise be
// pushed through it.
func (s *GameState) separate() {
	a, b := s.Pushbox(0), s.Pushbox(1)

	// Vertical miss: one player is cleanly above the other, so they pass.
	if a.Y >= b.Y+b.H || b.Y >= a.Y+a.H {
		return
	}

	overlap := min(a.X+a.W, b.X+b.W) - max(a.X, b.X)
	if overlap <= 0 {
		return
	}

	// The tie at identical positions breaks by player index, which is the only
	// property that matters: it breaks the same way on every machine.
	push := overlap / 2
	if s.Players[0].X <= s.Players[1].X {
		s.Players[0].X -= push
		s.Players[1].X += push
	} else {
		s.Players[0].X += push
		s.Players[1].X -= push
	}
}

// clampToStage keeps both pushboxes inside the walls.
func (s *GameState) clampToStage() {
	for i := range s.Players {
		p := &s.Players[i]
		box := s.Pushbox(i)
		if d := -StageHalfWidth - box.X; d > 0 {
			p.X += d
		}
		if d := (box.X + box.W) - StageHalfWidth; d > 0 {
			p.X -= d
		}
	}
}

// updateFacing turns each player toward the other.
//
// Facing is not cosmetic. Everything facing-relative inverts with it: walk
// directions, motion recognition, which way boxes mirror, and — the one that
// bites — which direction is "away" and therefore blocks. A player who has been
// crossed up and still faces the old way cannot block at all.
//
// Only actionable grounded states turn. You do not pivot in the middle of an
// attack, in hitstun, during a dash, or in the air: keeping your facing through
// the whole jump is exactly what makes a crossup a crossup.
//
// Run after positions are final and before hit detection, so an attacker whose
// opponent just jumped over them still has boxes on the side they committed to,
// and the defender's hurtbox is already on the correct side.
func (s *GameState) updateFacing() {
	for i := range s.Players {
		p := &s.Players[i]
		if !Actionable(p.State) {
			continue
		}
		switch other := s.Players[1-i].X; {
		case other > p.X:
			p.Facing = 1
		case other < p.X:
			p.Facing = -1
			// Exactly level: keep the current facing. Picking a side here would
			// flip both players back and forth on a shared pixel, and it would
			// have to pick the same one on both machines for no benefit.
		}
	}
}

// updateCamera centres the camera between the players and keeps it inside the
// stage. Derived from positions and stored, so it is identical on both machines
// and rolls back with everything else.
func (s *GameState) updateCamera() {
	mid := (s.Players[0].X + s.Players[1].X) / 2

	// Never show past a wall: the corner has to look like a corner.
	if lo := -StageHalfWidth + CameraHalfWidth; mid < lo {
		mid = lo
	}
	if hi := StageHalfWidth - CameraHalfWidth; mid > hi {
		mid = hi
	}
	// A stage narrower than the view pins the camera at the centre rather than
	// letting the two clamps fight.
	if CameraHalfWidth >= StageHalfWidth {
		mid = 0
	}
	s.CamX = mid
}
