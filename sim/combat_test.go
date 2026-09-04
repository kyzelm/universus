package sim

import "testing"

// facing puts the two players at a distance where move 0 reaches, both on the
// ground, facing each other. Returns the state ready for frame 1.
func facing(gap int) GameState {
	s := New()
	s.Players[0].X = FromInt(-gap / 2)
	s.Players[1].X = FromInt(gap / 2)
	return s
}

func TestBoxMirrorsWithFacing(t *testing.T) {
	b := Box{X: FromInt(10), Y: FromInt(5), W: FromInt(20), H: FromInt(8)}

	r := b.World(0, 0, 1)
	if r.X != FromInt(10) || r.W != FromInt(20) {
		t.Errorf("facing right: %+v", r)
	}
	// Facing left, the same local box occupies the mirrored span.
	l := b.World(0, 0, -1)
	if l.X != FromInt(-30) || l.W != FromInt(20) {
		t.Errorf("facing left: %+v", l)
	}
	// Y is unaffected by facing.
	if l.Y != r.Y || l.H != r.H {
		t.Errorf("facing changed the vertical extent: %+v vs %+v", l, r)
	}
}

func TestBoxOverlapEdges(t *testing.T) {
	b := Box{X: 0, Y: 0, W: FromInt(10), H: FromInt(10)}

	for _, tc := range []struct {
		name string
		o    Box
		want bool
	}{
		{"identical", b, true},
		{"contained", Box{X: FromInt(2), Y: FromInt(2), W: FromInt(2), H: FromInt(2)}, true},
		{"overlapping corner", Box{X: FromInt(9), Y: FromInt(9), W: FromInt(5), H: FromInt(5)}, true},
		// Touching edges are a miss: a hit must not depend on the last bit of
		// a position, and "exactly adjacent" is the case that would.
		{"touching right edge", Box{X: FromInt(10), Y: 0, W: FromInt(5), H: FromInt(5)}, false},
		{"touching top edge", Box{X: 0, Y: FromInt(10), W: FromInt(5), H: FromInt(5)}, false},
		{"one unit apart", Box{X: FromInt(11), Y: 0, W: FromInt(5), H: FromInt(5)}, false},
		{"zero width", Box{X: 0, Y: 0, W: 0, H: FromInt(5)}, false},
		{"negative height", Box{X: 0, Y: 0, W: FromInt(5), H: -One}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := b.Overlaps(tc.o); got != tc.want {
				t.Errorf("Overlaps = %v, want %v", got, tc.want)
			}
			if got := tc.o.Overlaps(b); got != tc.want {
				t.Errorf("Overlaps is not symmetric: reverse = %v, want %v", got, tc.want)
			}
		})
	}
}

// A hitbox exists only during the active window, and a hurtbox almost always.
func TestBoxesFollowTheFrameData(t *testing.T) {
	s := New()
	mv := &char().Moves[0]

	var boxes [MaxBoxes]Box
	s.Advance([2]uint16{InLP, 0})

	for f := int32(0); f < mv.Total(); f++ {
		state := s.Players[0]
		if state.State != StateAttack {
			t.Fatalf("frame %d: left the attack early, state = %d", f, state.State)
		}
		active := state.StateFrame >= mv.Startup && state.StateFrame < mv.Startup+mv.Active

		n := s.Hitboxes(0, &boxes)
		if active && n == 0 {
			t.Errorf("move frame %d is active but has no hitbox", state.StateFrame)
		}
		if !active && n != 0 {
			t.Errorf("move frame %d is not active but has %d hitbox(es)", state.StateFrame, n)
		}
		if s.Hurtboxes(0, &boxes) == 0 {
			t.Errorf("move frame %d has no hurtbox", state.StateFrame)
		}
		s.Advance([2]uint16{0, 0})
	}
}

func TestAttackConnectsAndDealsDamage(t *testing.T) {
	s := facing(30)
	mv := &char().Moves[0]
	full := s.Players[1].Health

	// Press, then hold nothing; the move runs to completion on its own.
	s.Advance([2]uint16{InLP, 0})
	for range mv.Total() + int32(mv.Hitstop) + 2 {
		s.Advance([2]uint16{0, 0})
	}

	// The jab is a light, so its 100 base arrives as 80 — the starter scaling
	// applies to the opening hit of a combo as much as to the rest of it.
	if got := s.Players[1].Health; got != full-jabFirstHit {
		t.Errorf("health = %d, want %d after the jab", got, full-jabFirstHit)
	}
	if s.Players[1].State != StateHitstun && s.Players[1].Stun == 0 {
		t.Errorf("defender is not in hitstun: state = %d stun = %d", s.Players[1].State, s.Players[1].Stun)
	}
}

// One active window, one hit. Without this a three-frame active window deals
// its damage three times.
func TestOneActiveWindowHitsOnce(t *testing.T) {
	s := facing(30)
	full := s.Players[1].Health

	s.Advance([2]uint16{InLP, 0})
	for range 60 {
		s.Advance([2]uint16{0, 0})
	}

	if got := full - s.Players[1].Health; got != jabFirstHit {
		t.Errorf("total damage %d, want %d — the active window hit more than once", got, jabFirstHit)
	}
}

// Out of range is a whiff, which is the other half of the box test working.
func TestAttackOutOfRangeMisses(t *testing.T) {
	s := facing(300)
	full := s.Players[1].Health

	s.Advance([2]uint16{InLP, 0})
	for range 60 {
		s.Advance([2]uint16{0, 0})
	}

	if s.Players[1].Health != full {
		t.Error("an attack at 300 units connected")
	}
}

func TestBlockingCostsNoHealthAndGivesBlockstun(t *testing.T) {
	s := facing(30)
	mv := &char().Moves[0]
	full := s.Players[1].Health

	// Player 1 faces left, so holding right is holding away. Stop on the frame
	// the block lands: blockstun is 11 frames and checking after the move has
	// fully recovered would find it already expired, which would pass for a
	// build that produced no blockstun at all.
	s.Advance([2]uint16{InLP, InRight})
	blocked := false
	for range 60 {
		s.Advance([2]uint16{0, InRight})
		if s.Players[1].State == StateBlockstun {
			blocked = true
			break
		}
		if s.Players[1].State == StateHitstun {
			t.Fatal("holding away produced hitstun, not blockstun")
		}
	}

	if !blocked {
		t.Fatal("the attack never connected, so nothing was blocked")
	}
	if s.Players[1].Health != full {
		t.Errorf("blocking cost %d health", full-s.Players[1].Health)
	}
	if s.Players[1].Stun != mv.Blockstun {
		t.Errorf("blockstun = %d, want the move's %d", s.Players[1].Stun, mv.Blockstun)
	}
}

// Standing blocks mids and highs; a low must be blocked crouching. Getting this
// wrong makes one of the two attack levels meaningless.
func TestBlockLevels(t *testing.T) {
	low := &char().Moves[1] // crouching low, must be blocked crouching

	for _, tc := range []struct {
		name    string
		defend  uint16
		blocked bool
	}{
		{"standing against a low", InRight, false},
		{"crouching against a low", InRight | InDown, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := facing(30)
			full := s.Players[1].Health

			// Player 0 crouches to get the low out.
			s.Advance([2]uint16{InDown | InLK, tc.defend})
			for range low.Total() + int32(low.Hitstop) + 2 {
				s.Advance([2]uint16{0, tc.defend})
			}

			hurt := s.Players[1].Health < full
			if tc.blocked && hurt {
				t.Errorf("the low was not blocked: health %d -> %d", full, s.Players[1].Health)
			}
			if !tc.blocked && !hurt {
				t.Error("the low was blocked by a standing defender")
			}
		})
	}
}

// Hitstop freezes both fighters. It is not cosmetic: it changes when the next
// action can happen, which is why it is in the sim at all.
func TestHitstopFreezesBothFighters(t *testing.T) {
	s := facing(30)
	mv := &char().Moves[0]

	for range 60 {
		s.Advance([2]uint16{InLP, 0})
		if s.Hitstop > 0 {
			break
		}
	}
	if s.Hitstop == 0 {
		t.Fatal("no hitstop after a connect")
	}
	if s.Hitstop != mv.Hitstop {
		t.Errorf("hitstop = %d, want the move's %d", s.Hitstop, mv.Hitstop)
	}

	before := s
	for s.Hitstop > 0 {
		s.Advance([2]uint16{InRight, InLeft}) // both mashing: nothing may move
	}

	for i := range s.Players {
		if s.Players[i].X != before.Players[i].X || s.Players[i].StateFrame != before.Players[i].StateFrame {
			t.Errorf("player %d moved during hitstop: %+v -> %+v", i, before.Players[i], s.Players[i])
		}
	}
	// The frame counter still runs, or the network and the checksum stall.
	if s.Frame == before.Frame {
		t.Error("the frame counter stopped during hitstop")
	}
}

// A trade is both attacks landing on the same frame. The fixed P1-then-P2 order
// exists so this resolves identically on both machines instead of depending on
// which player the loop reached first.
func TestSimultaneousHitsTrade(t *testing.T) {
	s := facing(30)
	mv := &char().Moves[0]
	full := [2]int32{s.Players[0].Health, s.Players[1].Health}

	// Both press on the same frame, so both active windows line up.
	s.Advance([2]uint16{InLP, InLP})
	for range mv.Total() + int32(mv.Hitstop) + 2 {
		s.Advance([2]uint16{0, 0})
	}

	// Both were attacking when they were hit, so both hits are counter hits:
	// the jab's 80 at the fixture's 150%. A trade is the one exchange where
	// that is true of both players at once.
	const traded = jabFirstHit * 150 / 100
	for i := range s.Players {
		if got := full[i] - s.Players[i].Health; got != traded {
			t.Errorf("player %d took %d in the trade, want %d — both should be hit", i, got, traded)
		}
	}
}

// Detection is separated from resolution so both directions see the same
// positions. If resolution ran inline, player 0's hit would put player 1 in
// hitstun before player 1's hitboxes were ever tested, and the trade above
// would silently become a one-sided hit.
func TestTradeDoesNotDependOnPlayerOrder(t *testing.T) {
	mirror := func(s GameState) GameState {
		s.Players[0], s.Players[1] = s.Players[1], s.Players[0]
		return s
	}

	a := facing(30)
	b := mirror(facing(30))

	for range 40 {
		a.Advance([2]uint16{InLP, InLP})
		b.Advance([2]uint16{InLP, InLP})
	}

	if a.Players[0].Health != b.Players[1].Health || a.Players[1].Health != b.Players[0].Health {
		t.Errorf("swapping the players changed the outcome: %d/%d vs %d/%d",
			a.Players[0].Health, a.Players[1].Health, b.Players[0].Health, b.Players[1].Health)
	}
}

func TestHealthNeverGoesNegative(t *testing.T) {
	s := facing(30)
	s.Players[1].Health = 1

	for range 200 {
		s.Advance([2]uint16{InLP, 0})
	}
	if s.Players[1].Health < 0 {
		t.Errorf("health = %d", s.Players[1].Health)
	}
}

// Frame advantage is computed from the frame data, never authored. A
// hand-entered number drifts the moment anything is tuned.
func TestAdvantageIsDerived(t *testing.T) {
	m := Move{Startup: 4, Active: 3, Recovery: 6, Hitstun: 14, Blockstun: 11}
	if got, want := m.Total(), int32(13); got != want {
		t.Errorf("Total = %d, want %d", got, want)
	}
	if got, want := m.OnHit(), int32(6); got != want {
		t.Errorf("OnHit = %d, want %d", got, want)
	}
	if got, want := m.OnBlock(), int32(3); got != want {
		t.Errorf("OnBlock = %d, want %d", got, want)
	}
}

// Sparse keyframes: an entry persists until the next one overrides it.
func TestBoxesAtPersistsBetweenKeyframes(t *testing.T) {
	mv := &char().Moves[0]

	if k := mv.BoxesAt(-1); k != nil {
		t.Error("a frame before the first keyframe should have no boxes")
	}
	first := mv.BoxesAt(0)
	if first == nil || first.Frame != 0 {
		t.Fatalf("frame 0 = %+v", first)
	}
	// Frames 1..3 have no keyframe of their own and keep frame 0's boxes.
	for f := int32(1); f < mv.Startup; f++ {
		if got := mv.BoxesAt(f); got != first {
			t.Errorf("frame %d did not keep the frame 0 keyframe", f)
		}
	}
	if got := mv.BoxesAt(mv.Startup); got == first {
		t.Error("the startup keyframe did not take over")
	}
}

// Facing is not cosmetic: it decides which way is forward, which way motions
// read, which way boxes mirror, and which direction blocks. A player who has
// been crossed up and still faces the old way cannot block at all.
func TestPlayersTurnToFaceEachOther(t *testing.T) {
	s := New()
	s.Players[0].X = FromInt(40)
	s.Players[1].X = FromInt(-40)

	s.Advance([2]uint16{0, 0})

	if s.Players[0].Facing != -1 {
		t.Errorf("player 0 is on the right but faces %d", s.Players[0].Facing)
	}
	if s.Players[1].Facing != 1 {
		t.Errorf("player 1 is on the left but faces %d", s.Players[1].Facing)
	}
}

// Keeping your facing through the whole jump is what makes a crossup a crossup:
// turning mid-air would flip the attacker's boxes as they passed overhead.
func TestFacingHoldsThroughAJumpAndTurnsOnLanding(t *testing.T) {
	s := New()
	s.Players[0].X = FromInt(-10)
	s.Players[1].X = FromInt(10)

	hold(&s, int(char().PreJumpFrames), InUp|InRight)
	crossed := false
	for range 80 {
		s.Advance([2]uint16{InRight, 0})
		if s.Players[0].Airborne() {
			if s.Players[0].X > s.Players[1].X {
				crossed = true
				if s.Players[0].Facing != 1 {
					t.Fatal("player 0 turned around in mid-air")
				}
			}
		} else if crossed {
			break
		}
	}

	if !crossed {
		t.Skip("the jump did not clear the opponent; nothing to assert")
	}
	if s.Players[0].Facing != -1 {
		t.Errorf("player 0 landed on the far side still facing %d", s.Players[0].Facing)
	}
}

// A player mid-attack does not pivot: the move commits to the side it started on.
func TestAttackerDoesNotTurnMidMove(t *testing.T) {
	s := facing(30)
	s.Advance([2]uint16{InLP, 0})
	if s.Players[0].State != StateAttack {
		t.Fatal("setup: no attack")
	}

	// Teleport the opponent behind the attacker mid-move.
	s.Players[1].X = s.Players[0].X - FromInt(60)
	want := s.Players[0].Facing
	s.Advance([2]uint16{0, 0})

	if s.Players[0].Facing != want {
		t.Errorf("the attacker pivoted mid-move: %d -> %d", want, s.Players[0].Facing)
	}
}

// The bug this pair exists to catch: after a side swap, "hold away" must still
// be away. Blocking is read from facing, so a stale facing means the defender
// eats everything while holding what looks like block.
func TestBlockingWorksAfterASideSwap(t *testing.T) {
	s := New()
	// Swap them: player 1 is now on the left, so its "away" is right-to-left.
	s.Players[0].X = FromInt(15)
	s.Players[1].X = FromInt(-15)
	s.Advance([2]uint16{0, 0}) // one frame to turn

	full := s.Players[1].Health
	// Player 1 now faces right, so holding LEFT is holding away.
	blocked := false
	s.Advance([2]uint16{InLP, InLeft})
	for range 40 {
		s.Advance([2]uint16{0, InLeft})
		if s.Players[1].State == StateBlockstun {
			blocked = true
			break
		}
		if s.Players[1].State == StateHitstun {
			t.Fatal("holding away after a side swap took a hit: facing is stale")
		}
	}
	if !blocked {
		t.Fatal("the attack never connected after the swap")
	}
	if s.Players[1].Health != full {
		t.Errorf("blocking cost %d health", full-s.Players[1].Health)
	}
}

// Crouch-blocking: down-back blocks both lows and mids, down alone blocks
// nothing. This is the combination that is easy to get wrong by treating
// "crouching" and "holding back" as separate states.
func TestCrouchBlockingCoversLowsAndMids(t *testing.T) {
	for _, tc := range []struct {
		name    string
		move    int32
		attack  uint16
		defend  uint16
		blocked bool
	}{
		{"down-back vs mid", 0, InLP, InRight | InDown, true},
		{"down-back vs low", 1, InDown | InLK, InRight | InDown, true},
		{"down alone vs mid", 0, InLP, InDown, false},
		{"standing back vs low", 1, InDown | InLK, InRight, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := facing(30)
			full := s.Players[1].Health
			mv := &char().Moves[tc.move]

			s.Advance([2]uint16{tc.attack, tc.defend})
			for range mv.Total() + mv.Hitstop + 4 {
				s.Advance([2]uint16{0, tc.defend})
			}

			hurt := s.Players[1].Health < full
			if tc.blocked && hurt {
				t.Errorf("should have blocked, took %d", full-s.Players[1].Health)
			}
			if !tc.blocked && !hurt {
				t.Error("should have been hit, took nothing")
			}
		})
	}
}

// Invincible frames are the absence of hurtboxes, and Hurtboxes is the one
// funnel every attack and every projectile asks. A move inside its window
// answers nothing, so there is nothing to hit.
func TestInvulnerableFramesHaveNoHurtboxes(t *testing.T) {
	s := New()
	mv := &char().Moves[7]
	s.Advance([2]uint16{InMP, 0})

	var boxes [MaxBoxes]Box
	for range mv.Total() {
		p := &s.Players[0]
		n := s.Hurtboxes(0, &boxes)
		if inside := p.StateFrame < mv.InvulnEnd; inside != (n == 0) {
			t.Fatalf("frame %d of the move: %d hurtboxes, invulnerable window is [%d, %d)",
				p.StateFrame, n, mv.InvulnStart, mv.InvulnEnd)
		}
		s.Advance([2]uint16{0, 0})
	}
}

// The point of the window: a reversal beats an attack that would beat it on
// frames alone. The jab is faster and connects first; the reversal is not there
// to be hit, and comes back down on the player who threw it.
func TestAReversalBeatsAFasterAttack(t *testing.T) {
	s := facing(30)
	rev, jab := &char().Moves[7], &char().Moves[0]
	full := [2]int32{s.Players[0].Health, s.Players[1].Health}

	// Same frame, so the jab's active frames 4..6 land inside the reversal's
	// invulnerable window and the reversal's own hitbox arrives on frame 5.
	s.Advance([2]uint16{InMP, InLP})
	for range 60 {
		s.Advance([2]uint16{0, 0})
	}

	if got := full[0] - s.Players[0].Health; got != 0 {
		t.Errorf("the reversal took %d damage during its invulnerable frames", got)
	}
	// The reversal's 250, scaled by its medium starter and paid at counter-hit
	// rates — it landed on a player who was mid-jab, which is what a counter
	// hit is.
	const punished = 250 * 90 / 100 * 150 / 100
	if got := full[1] - s.Players[1].Health; got != punished {
		t.Errorf("the jab player took %d, want the reversal's %d", got, punished)
	}
	if jab.Startup >= rev.Startup {
		t.Fatalf("fixture: the jab (%d) must be faster than the reversal (%d) or this proves nothing",
			jab.Startup, rev.Startup)
	}
}

// And the other half: past the window the same attack connects. Otherwise the
// test above would pass on a move that simply never gets hit.
func TestTheWindowEnds(t *testing.T) {
	s := facing(300) // far enough that the reversal itself whiffs
	rev := &char().Moves[7]
	full := s.Players[0].Health

	s.Advance([2]uint16{InMP, 0})
	// Wait out the window and the reversal's own active frames, so what is
	// under test is the hurtbox coming back and not a trade.
	for s.Players[0].StateFrame < rev.Startup+rev.Active {
		s.Advance([2]uint16{0, 0})
	}
	if s.Players[0].StateFrame < rev.InvulnEnd {
		t.Fatal("fixture: the window outlasts the active frames")
	}

	// Walk the jab into range now that the invulnerable frames are behind it.
	s.Players[0].X, s.Players[1].X = FromInt(-15), FromInt(15)
	s.Advance([2]uint16{0, InLP})
	for range 60 {
		s.Advance([2]uint16{0, 0})
	}

	if s.Players[0].Health == full {
		t.Error("the reversal was still invulnerable after its window ended")
	}
}
