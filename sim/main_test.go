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
	os.Exit(m.Run())
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

		NumMoves: 3,
	}

	// A standing jab: 4 startup, 3 active, 6 recovery. Reaches 40 units, which
	// is further than the players start apart in the hit tests below.
	c.Moves[0] = Move{
		Startup: 4, Active: 3, Recovery: 6,
		Damage: 100, Hitstun: 14, Blockstun: 11, Hitstop: 6,
		Level: LevelMid, Stance: StanceStand, Button: InLP,
		NumKeys: 3,
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
	c.Moves[1] = Move{
		Startup: 5, Active: 2, Recovery: 9,
		Damage: 80, Hitstun: 12, Blockstun: 9, Hitstop: 5,
		Level: LevelLow, Stance: StanceCrouch, Button: InLK,
		NumKeys: 2,
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

	return c
}

// char is the fixture as the sim sees it, for tests that need its numbers.
func char() *Character { return CharacterAt(0) }
