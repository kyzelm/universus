package sim

// The scripted opponent (03 Game Design/AI Opponent.md).
//
// **It emits input bitfields and nothing else.** No direct state access on the
// way out, no privileged actions, no reading the other player's intentions —
// it decides from what it can see and then presses buttons, through the same
// pipeline a keyboard presses them through. That is not purism: it is what
// makes an AI match replay exactly, what lets two of these feed the bot-vs-bot
// network harness, and what makes cheating impossible by construction, so
// difficulty has to come from behaviour rather than from privilege.
//
// **One rule list, two ways in.** A seat the sim plays (aiInput, called from
// Advance) keeps its plan in GameState and draws from the match's own PRNG, so
// every decision rolls back and replays like everything else. A seat played
// from *outside* (AIDevice) keeps both privately and writes nothing to the
// state — which is what the bot-vs-bot network harness needs, because two ends
// that each generated the opponent's inputs locally would never mispredict, and
// a harness that never mispredicts tests none of the netcode it exists for.
//
// The rules themselves are written once, against an interface for the rolls, so
// the opponent the harness fuzzes is the opponent somebody plays.
//
// Honest about what it is: a priority-ordered rule list, first match wins. Not
// a behaviour tree, not a search, and nothing that learns — that boundary is
// chosen rather than hit (D42), and the engineering value is in the
// input-level integration rather than in the decision logic.

// Difficulty tiers. The index into balance.AITiers, with AIOff for a seat a
// human is playing.
//
// Difficulty is reaction delay and decision randomness **only** (D63): never
// extra damage, never extra meter, never frame-perfect inputs. An AI with
// privileges is transparently unfair and teaches the player nothing.
const (
	AIOff int32 = iota
	AIEasy
	AINormal
	AIHard
	aiTierCount = 3
)

// The plans. A decision picks one and it runs until the next decision, which
// is what keeps the AI from re-deciding sixty times a second — a human cannot,
// and an opponent that does reads as a machine.
//
// A plan is a *function of the frames since it started*, not a stored script:
// the motions below are three frames long and everything else is a hold, so
// there is nothing to keep in state but which plan is running.
const (
	aiNeutral int32 = iota
	aiApproach
	aiRetreat
	aiBlock
	aiJab
	aiPunish
	aiSweep
	aiDP
	aiFireball
	aiThrow
	aiJump
	aiPlanCount
)

// aiRandom is where a decision's rolls come from. Two implementations and no
// more: the match's own generator for a seat the sim is playing, and a device's
// private one for a seat it is playing from outside (see AIDevice).
//
// The rule list is written against this rather than against GameState so that
// both entry points run *the same rules*. A second copy of the list for the
// harness would be a second opponent, and the one the tests fuzzed would not be
// the one anybody plays.
type aiRandom interface {
	RandN(n uint32) uint32
}

// AIDevice plays a seat from outside the simulation: the same rule list, its
// own generator and its own plan, and **nothing written to GameState**.
//
// It exists for the bot-vs-bot network harness (D49). A seat the sim plays is
// no use there: both ends would generate the identical inputs locally, every
// prediction would be right, and a harness that never mispredicts tests none of
// the netcode it was built to test. A device presses buttons on one end and
// they cross the wire like a player's.
//
// Its state is deliberately outside the match: it is not checksummed, so two
// ends running different devices — or one running none — is not a desync, and
// a rollback replays the inputs it already sent rather than asking it again.
type AIDevice struct {
	tier int32
	plan int32
	rng  uint32
}

// NewAIDevice returns a device at the given tier. The seed is what makes two of
// them play differently; the same seed twice is the same opponent twice, which
// is what makes a harness run reproducible.
func NewAIDevice(tier int32, seed uint32) *AIDevice {
	if seed == 0 {
		seed = 1
	}
	return &AIDevice{tier: tier, rng: seed}
}

// RandN is the device's own generator — the same xorshift the sim uses, kept
// separate so a device draw never perturbs the match's sequence.
func (d *AIDevice) RandN(n uint32) uint32 {
	x := d.rng
	x ^= x << 13
	x ^= x >> 17
	x ^= x << 5
	d.rng = x
	if n == 0 {
		return 0
	}
	return uint32((uint64(x) * uint64(n)) >> 32)
}

// Press is one frame of input for a seat, decided from the state as it stands.
// The state is read and never written.
func (d *AIDevice) Press(s *GameState, seat int) uint16 {
	every := balance.AIDecisionFrames
	if every <= 0 || seat < 0 || seat > 1 {
		return 0
	}
	elapsed := int32(s.Frame % uint32(every))
	if elapsed == 0 {
		d.plan = aiDecide(s, seat, d.tier, d)
	}
	return aiPress(d.plan, elapsed, s.Players[seat].Facing)
}

// aiInput is the bitfield this seat presses this frame.
//
// Called from Advance before the input history is recorded, so the AI's presses
// go into the ring like a human's and motion recognition, the input buffer and
// the dash double-tap all read them the same way.
func (s *GameState) aiInput(i int) uint16 {
	p := &s.Players[i]
	if p.AI == AIOff {
		return 0
	}

	// Decisions are on a fixed cadence off the frame number rather than a
	// countdown in state: one fewer field, and it rolls back for free because
	// the frame number does.
	every := balance.AIDecisionFrames
	if every <= 0 {
		return 0 // an unloaded balance is a still opponent, not a random one
	}
	elapsed := int32(s.Frame % uint32(every))
	if elapsed == 0 {
		p.AIPlan = aiDecide(s, i, p.AI, s)
	}
	return aiPress(p.AIPlan, elapsed, p.Facing)
}

// aiPress turns a plan and its age into buttons. Directions are absolute, like
// every other device's: forward is a facing question and the sim answers it.
func aiPress(plan, age int32, facing int32) uint16 {
	fwd, back := InRight, InLeft
	if facing < 0 {
		fwd, back = InLeft, InRight
	}

	switch plan {
	case aiApproach:
		return fwd
	case aiRetreat, aiBlock:
		// Blocking *is* holding away, so the plan that blocks and the plan that
		// backs off are the same input. What separates them is why they were
		// chosen, and the AI does not need to remember which it meant.
		return back
	case aiJab:
		return press(age, InLP)
	case aiPunish:
		return press(age, InHP)
	case aiSweep:
		return press(age, InDown|InHK)
	case aiThrow:
		return press(age, InLP|InLK)
	case aiJump:
		// Pressed, not held: holding up would jump again on every landing,
		// which is a plan nobody chose.
		return press(age, InUp)
	case aiDP:
		// **The shortcut motion, → ↓ →, and deliberately not the real → ↓ ↘.**
		// Two dragon punches in a row spell a double quarter-circle across the
		// gap between them — ↓ ↘ → ↓ ↘ → is inside → ↓ ↘ → ↓ ↘ → — and the
		// supers are scanned first (D78), so the second anti-air would come out
		// as a level 1 whenever the gauge happened to be full. The shortcut row
		// has no diagonal in it, so no quarter-circle can be read out of any
		// number of them (D96).
		switch age {
		case 0:
			return fwd
		case 1:
			return InDown
		case 2:
			return fwd | InLP
		}
	case aiFireball:
		switch age {
		case 0:
			return InDown
		case 1:
			return InDown | fwd
		case 2:
			return fwd | InLP
		}
	}
	return 0
}

// press is one frame of a button and then nothing: a held button is one press
// to the sim, and a plan that kept holding would eat its own next decision.
func press(age int32, bits uint16) uint16 {
	if age == 0 {
		return bits
	}
	return 0
}

// aiDecide is the rule list, in priority order, first match wins. The order is
// the design note's own and is the whole of the AI's character: anti-air before
// punish, punish before block, and neutral movement last.
func aiDecide(s *GameState, i int, tier int32, r aiRandom) int32 {
	p, o := &s.Players[i], &s.Players[1-i]
	t := &balance.AITiers[aiTier(tier)]
	dist := s.aiDistance(i)

	// **Deliberate imperfection, first and on purpose.** An opponent that only
	// ever plays correctly is both frustrating and obviously artificial, so the
	// roll that throws the rule list away entirely is the one that makes the AI
	// whiff, press in a bad spot and fail to punish. It is also the second of
	// the two difficulty levers and the only one that is not reaction time.
	if aiRoll(r, t.RandomPercent) {
		return int32(r.RandN(uint32(aiPlanCount)))
	}

	switch {
	// Anti-air. The window is generous on purpose: a dragon punch that comes
	// out late loses to the jump-in it was meant to beat, and that is the
	// mistake a human makes too.
	case o.Airborne() && dist <= balance.AIAntiAirRange && aiSeen(o, t.Reaction):
		return aiDP

	// Punish. A move in its recovery cannot block, and the AI's heaviest
	// button is the answer.
	//
	// ponytail: one button, not a combo. Scripted combo sequences are the
	// design note's own next step and they need a plan that outlives one
	// decision; a heavy is a punish that hurts and the mechanism is the same.
	case aiRecovering(o) && dist <= balance.AICloseRange && aiSeen(o, t.Reaction):
		return aiPunish

	// Block what is coming, as often as the tier says. The roll is what makes
	// Easy beatable and Hard oppressive without giving either of them anything
	// the player does not have.
	case aiIncoming(o) && dist <= balance.AIMidRange &&
		aiSeen(o, t.Reaction) && aiRoll(r, t.BlockPercent):
		return aiBlock

	// Reversal out of pressure. Pressed *during* blockstun: the input buffer
	// holds it and spends it on the first actionable frame, which is exactly
	// how a player reverses (D83), and it is why nothing here needs to know how
	// long the stun is.
	case p.State == StateBlockstun && aiRoll(r, balance.AIReversalPercent):
		return aiDP

	// Fireball at range, and only with the screen clear of its own. One at a
	// time is how the move is used, and it spaces the motions far enough apart
	// that two of them cannot be read as a super.
	case dist > balance.AIMidRange && !s.aiHasProjectile(i) &&
		aiRoll(r, balance.AIProjectilePercent):
		return aiFireball

	// Mid range: close the gap. This is what stops two of these standing at
	// opposite ends of the stage for ninety-nine seconds.
	case dist > balance.AICloseRange:
		return aiApproach

	// Close, and they are holding a block. A throw is what beats blocking, and
	// the AI knowing that is the difference between pressure and a jab loop.
	case o.State == StateBlockstun && aiRoll(r, balance.AIThrowPercent):
		return aiThrow

	// Close and nothing else applies: press something small, back off, or
	// stand. The mix is what makes the neutral look like a player.
	default:
		switch r.RandN(4) {
		case 0:
			return aiJab
		case 1:
			return aiSweep
		case 2:
			return aiRetreat
		}
		return aiNeutral
	}
}

// aiTier clamps a difficulty to a tier index, so a hand-built state with a
// nonsense value plays like Normal rather than reading past the array.
func aiTier(ai int32) int32 {
	if ai < AIEasy || ai > AIHard {
		return AINormal - 1
	}
	return ai - 1
}

// aiRoll is a percentage chance. The generator is the caller's: the match's own
// for a seat the sim plays, so every decision rolls back and replays exactly,
// and the device's own for a seat played from outside.
func aiRoll(r aiRandom, percent int32) bool {
	if percent <= 0 {
		return false
	}
	return int32(r.RandN(100)) < percent
}

// aiSeen models perception latency, which is the honest difficulty lever: the
// AI may not react to something until it has been true for `reaction` frames.
//
// A state frame count rather than a queue of past states: what the AI reacts to
// is always something the opponent has just started doing, and how long they
// have been doing it is already in the state. A 0-frame AI blocks everything
// and is no fun; ~16 frames is roughly human.
func aiSeen(o *PlayerState, reaction int32) bool { return o.StateFrame >= reaction }

// aiIncoming is an attack that has not yet connected — something still worth
// blocking.
func aiIncoming(o *PlayerState) bool { return o.State == StateAttack && o.HasHit == 0 }

// aiRecovering is an attack past its active frames: the punish window, and the
// only time an attacker cannot block.
func aiRecovering(o *PlayerState) bool {
	if o.State != StateAttack {
		return false
	}
	mv := o.move()
	return mv != nil && o.StateFrame >= mv.Startup+mv.Active
}

// aiDistance is the gap between the two fighters, always positive.
func (s *GameState) aiDistance(i int) Fix {
	d := s.Players[1-i].X - s.Players[i].X
	if d < 0 {
		return -d
	}
	return d
}

// aiHasProjectile reports whether this player already has one on screen.
func (s *GameState) aiHasProjectile(i int) bool {
	for k := range s.Projectiles {
		if s.Projectiles[k].Active != 0 && s.Projectiles[k].Owner == int32(i) {
			return true
		}
	}
	return false
}
