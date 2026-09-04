package sim

import (
	"os"
	"testing"
)

// Sim tests run against a fixture, not against the shipped character file.
//
// They cannot import package data — that package imports this one — but the
// stronger reason is that a test asserting "a jump lasts 43 frames" against
// live balance data fails every time someone tunes a number, and the failure
// says nothing. The fixture uses round numbers chosen to make the arithmetic
// checkable by hand. data's own tests cover the shipped file.
func TestMain(m *testing.M) {
	if !LoadCharacters([]Character{testCharacter()}) {
		panic("fixture roster rejected")
	}
	LoadBalance(testBalance())
	os.Exit(m.Run())
}

// The balance fixture, in round numbers for the same reason as the character
// one: a test that asserts "eight blocked hits burn you out" must not fail
// because someone tuned the block cost.
//
// One bar per blocked hit, 100 units of regeneration a frame, and a Burnout
// that refills in exactly 20 frames.
func testBalance() Balance {
	return Balance{
		DriveRegen:      10,
		DriveRegenWalkF: 20,

		DriveRegenBurnout: DriveMax / 20,
		DriveBlockCost:    BarUnits,

		BurnoutBlockstun:   5,
		BurnoutChipPercent: 10,

		// The scaling table is the shipped shape; the numbers are round enough
		// to check by hand either way.
		ComboScale:       [ComboScaleSteps]int32{100, 100, 80, 70, 60, 50, 40, 30, 20, 10},
		StarterLight:     80,
		StarterMedium:    90,
		StarterHeavy:     100,
		MinDamagePercent: 10,

		// Deliberately not the shipped 125: a test that passes on the fixture
		// and on the live balance is a test that is reading neither.
		CounterHitPercent: 150,
		CounterHitstun:    5,
		PunishHitstun:     10,

		SuperDealtPercent: 50,
		SuperTakenPercent: 25,
		SuperOnSpecial:    200,
	}
}

func testCharacter() Character {
	c := Character{
		Health: 1000,

		WalkForward: One * 2,
		WalkBack:    One,

		DashDistance:   FromInt(40),
		DashFrames:     10,
		BackdashFrames: 20,

		JumpVelocity:  FromInt(8),
		JumpForwardVX: FromInt(2),
		Gravity:       -One / 2, // 8.0 up at 0.5 down: 32 frames airborne, exactly
		PreJumpFrames: 4,

		Pushbox:    Box{X: FromInt(-12), Y: 0, W: FromInt(24), H: FromInt(48)},
		StandHurt:  Box{X: FromInt(-12), Y: 0, W: FromInt(24), H: FromInt(48)},
		CrouchHurt: Box{X: FromInt(-12), Y: 0, W: FromInt(24), H: FromInt(32)},
		AirHurt:    Box{X: FromInt(-12), Y: FromInt(4), W: FromInt(24), H: FromInt(40)},

		NumMoves: 10,
	}

	// A standing jab: 4 startup, 3 active, 6 recovery. Reaches 40 units, which
	// is further than the players start apart in the hit tests below.
	// It also cancels into specials, which makes it the fixture's designated
	// cancelable normal: the jab and the special on move 2 share a button, so
	// the cancel is told from a fresh press by the motion and by nothing else.
	// It feeds the level 1 super as well — the design gives level 1 to
	// cancelable normals and to nothing else, and move 1 below is the source
	// that proves the tiering by not having it.
	c.Moves[0] = Move{
		Startup: 4, Active: 3, Recovery: 6,
		Damage: 100, Hitstun: 14, Blockstun: 11, Hitstop: 6,
		Level: LevelMid, Stance: StanceStand, Button: InLP,
		CancelInto: CancelSpecial | CancelSuper1 | CancelSuper3,
		NumKeys:    3,
	}
	c.Moves[0].Keys[0] = Keyframe{Frame: 0, NumHurt: 1}
	c.Moves[0].Keys[0].Hurt[0] = c.StandHurt
	c.Moves[0].Keys[1] = Keyframe{Frame: 4, NumHurt: 1, NumHit: 1}
	c.Moves[0].Keys[1].Hurt[0] = c.StandHurt
	c.Moves[0].Keys[1].Hit[0] = Box{X: FromInt(12), Y: FromInt(30), W: FromInt(28), H: FromInt(12)}
	c.Moves[0].Keys[2] = Keyframe{Frame: 7, NumHurt: 1}
	c.Moves[0].Keys[2].Hurt[0] = c.StandHurt

	// A crouching low, so the block-level tests have something that must be
	// blocked crouching.
	// It cancels into the level 3 super and nothing else, which is the heavy's
	// tier: level 3 comes out of anything, level 1 only out of the normals that
	// were cancelable to begin with.
	c.Moves[1] = Move{
		Startup: 5, Active: 2, Recovery: 9,
		Damage: 80, Hitstun: 12, Blockstun: 9, Hitstop: 5,
		Level: LevelLow, Stance: StanceCrouch, Button: InLK,
		CancelInto: CancelSuper3,
		NumKeys:    2,
	}
	c.Moves[1].Keys[0] = Keyframe{Frame: 0, NumHurt: 1}
	c.Moves[1].Keys[0].Hurt[0] = c.CrouchHurt
	c.Moves[1].Keys[1] = Keyframe{Frame: 5, NumHurt: 1, NumHit: 1}
	c.Moves[1].Keys[1].Hurt[0] = c.CrouchHurt
	c.Moves[1].Keys[1].Hit[0] = Box{X: FromInt(12), Y: FromInt(2), W: FromInt(28), H: FromInt(10)}

	// A special on the same button as the jab, which is the case that matters:
	// QCF+LP and LP are told apart by the motion and nothing else.
	c.Moves[2] = Move{
		Startup: 6, Active: 3, Recovery: 10,
		Damage: 200, Hitstun: 20, Blockstun: 14, Hitstop: 8,
		Level: LevelMid, Stance: StanceStand, Button: InLP, Motion: MotionQCF,
		NumKeys: 2,
	}
	c.Moves[2].Keys[0] = Keyframe{Frame: 0, NumHurt: 1}
	c.Moves[2].Keys[0].Hurt[0] = c.StandHurt
	c.Moves[2].Keys[1] = Keyframe{Frame: 6, NumHurt: 1}
	c.Moves[2].Keys[1].Hurt[0] = c.StandHurt

	// Everything that hits is the projectile; the move itself has no hitbox,
	// like the shipped fireball. Round numbers again: 4 units a frame for 60
	// frames is 240, more than the stage is wide.
	c.Moves[2].Proj = ProjectileSpec{
		Speed:  FromInt(4),
		Life:   60,
		SpawnX: FromInt(20),
		SpawnY: FromInt(20),
		Box:    Box{X: 0, Y: 0, W: FromInt(20), H: FromInt(14)},
	}

	// A launcher: rises on its own velocity, gravity brings it back. Airborne
	// for ~32 frames at 8.0 up and 0.5 down, and the move is longer than that,
	// so it lands with recovery to spare.
	c.Moves[3] = Move{
		Startup: 3, Active: 8, Recovery: 25,
		Damage: 300, Hitstun: 20, Blockstun: 14, Hitstop: 10,
		Level: LevelMid, Stance: StanceStand, Button: InHP,
		LaunchVX: FromInt(2), LaunchVY: FromInt(8),
		NumKeys: 2,
	}
	c.Moves[3].Keys[0] = Keyframe{Frame: 0, NumHurt: 1}
	c.Moves[3].Keys[0].Hurt[0] = c.StandHurt
	c.Moves[3].Keys[1] = Keyframe{Frame: 3, NumHurt: 1, NumHit: 1}
	c.Moves[3].Keys[1].Hurt[0] = c.AirHurt
	c.Moves[3].Keys[1].Hit[0] = Box{X: FromInt(4), Y: FromInt(28), W: FromInt(26), H: FromInt(30)}

	// The same, but far too short to land in: it exists to exercise a move that
	// runs out while the character is still in the air.
	// It owes landing recovery for it: the fall is not free just because the
	// move ended halfway up.
	c.Moves[4] = Move{
		Startup: 2, Active: 2, Recovery: 2,
		Damage: 100, Hitstun: 12, Blockstun: 9, Hitstop: 5,
		Level: LevelMid, Stance: StanceStand, Button: InHK,
		LaunchVY: FromInt(8),
		Landing:  5,
		NumKeys:  1,
	}
	c.Moves[4].Keys[0] = Keyframe{Frame: 0, NumHurt: 1}
	c.Moves[4].Keys[0].Hurt[0] = c.AirHurt

	// An advancing normal: horizontal only, never leaves the ground.
	c.Moves[5] = Move{
		Startup: 4, Active: 3, Recovery: 8,
		Damage: 150, Hitstun: 14, Blockstun: 11, Hitstop: 6,
		Level: LevelMid, Stance: StanceStand, Button: InMK,
		LaunchVX: FromInt(3),
		NumKeys:  1,
	}
	c.Moves[5].Keys[0] = Keyframe{Frame: 0, NumHurt: 1}
	c.Moves[5].Keys[0].Hurt[0] = c.StandHurt

	// An air normal. No launch: it rides the jump it came out of, and it costs
	// four frames on the way down.
	c.Moves[6] = Move{
		Startup: 3, Active: 4, Recovery: 8,
		Damage: 200, Hitstun: 16, Blockstun: 12, Hitstop: 8,
		Level: LevelHigh, Stance: StanceAir, Button: InLP,
		Landing: 4,
		NumKeys: 2,
	}
	c.Moves[6].Keys[0] = Keyframe{Frame: 0, NumHurt: 1}
	c.Moves[6].Keys[0].Hurt[0] = c.AirHurt
	c.Moves[6].Keys[1] = Keyframe{Frame: 3, NumHurt: 1, NumHit: 1}
	c.Moves[6].Keys[1].Hurt[0] = c.AirHurt
	c.Moves[6].Keys[1].Hit[0] = Box{X: FromInt(10), Y: FromInt(16), W: FromInt(26), H: FromInt(20)}

	// An invincible reversal: no hurtboxes at all for its startup and the first
	// two active frames, so an attack that would beat it on frames alone loses
	// to it anyway. On a real character this carries a DP motion; here it is on
	// its own button, because what is under test is the window and not the
	// recogniser, which has its own tests.
	c.Moves[7] = Move{
		Startup: 5, Active: 4, Recovery: 20,
		Damage: 250, Hitstun: 20, Blockstun: 14, Hitstop: 8,
		Level: LevelMid, Stance: StanceStand, Button: InMP,
		InvulnStart: 0, InvulnEnd: 7,
		NumKeys: 3,
	}
	c.Moves[7].Keys[0] = Keyframe{Frame: 0, NumHurt: 1}
	c.Moves[7].Keys[0].Hurt[0] = c.StandHurt
	c.Moves[7].Keys[1] = Keyframe{Frame: 5, NumHurt: 1, NumHit: 1}
	c.Moves[7].Keys[1].Hurt[0] = c.StandHurt
	c.Moves[7].Keys[1].Hit[0] = Box{X: FromInt(12), Y: FromInt(30), W: FromInt(28), H: FromInt(12)}
	// The hitbox goes away when the active window does: a keyframe persists
	// until the next one, so a move with nothing after its active frames keeps
	// swinging through its own recovery.
	c.Moves[7].Keys[2] = Keyframe{Frame: 9, NumHurt: 1}
	c.Moves[7].Keys[2].Hurt[0] = c.StandHurt

	// The level 1 super, on the jab's button and the fireball's motion doubled.
	// Three moves share InLP and only the motion and the meter tell them apart,
	// which is the whole selection rule in one button.
	c.Moves[8] = Move{
		Startup: 6, Active: 3, Recovery: 12,
		Damage: 400, Hitstun: 20, Blockstun: 14, Hitstop: 8,
		Level: LevelMid, Stance: StanceStand, Button: InLP, Motion: MotionQCFx2,
		Super:   1,
		NumKeys: 3,
	}
	c.Moves[8].Keys[0] = Keyframe{Frame: 0, NumHurt: 1}
	c.Moves[8].Keys[0].Hurt[0] = c.StandHurt
	c.Moves[8].Keys[1] = Keyframe{Frame: 6, NumHurt: 1, NumHit: 1}
	c.Moves[8].Keys[1].Hurt[0] = c.StandHurt
	c.Moves[8].Keys[1].Hit[0] = Box{X: FromInt(12), Y: FromInt(20), W: FromInt(30), H: FromInt(20)}
	c.Moves[8].Keys[2] = Keyframe{Frame: 9, NumHurt: 1}
	c.Moves[8].Keys[2].Hurt[0] = c.StandHurt

	// The level 3, on the other double motion so the two supers cannot be
	// confused for one another, and on the reversal's button so the pair also
	// covers "a motion move and a bare-button move sharing a button".
	c.Moves[9] = Move{
		Startup: 4, Active: 3, Recovery: 20,
		Damage: 900, Hitstun: 24, Blockstun: 16, Hitstop: 10,
		Level: LevelMid, Stance: StanceStand, Button: InMP, Motion: MotionQCBx2,
		Super:   3,
		NumKeys: 3,
	}
	c.Moves[9].Keys[0] = Keyframe{Frame: 0, NumHurt: 1}
	c.Moves[9].Keys[0].Hurt[0] = c.StandHurt
	c.Moves[9].Keys[1] = Keyframe{Frame: 4, NumHurt: 1, NumHit: 1}
	c.Moves[9].Keys[1].Hurt[0] = c.StandHurt
	c.Moves[9].Keys[1].Hit[0] = Box{X: FromInt(12), Y: FromInt(0), W: FromInt(34), H: FromInt(46)}
	c.Moves[9].Keys[2] = Keyframe{Frame: 7, NumHurt: 1}
	c.Moves[9].Keys[2].Hurt[0] = c.StandHurt

	return c
}

// jabFirstHit is what move 0 actually deals as the clean opening hit of a
// combo: 100 base, scaled by the light starter's 80% and by the first hit's
// 100%. Written out rather than computed, so a test asserting it cannot agree
// with a broken formula by running the same broken formula.
const jabFirstHit = 80

// char is the fixture as the sim sees it, for tests that need its numbers.
func char() *Character { return CharacterAt(0) }
