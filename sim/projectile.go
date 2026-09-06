package sim

// Projectiles. A fireball outlives the move that produced it — the character
// recovers, walks away, and the thing is still travelling — so it cannot be a
// hitbox hanging off the attacker. It is state, and because it is state it is
// in GameState: a projectile that does not roll back is a projectile that hits
// on one machine and misses on the other.

// MaxProjectiles is the pool for the whole match, both players. Fixed, because
// GameState has no allocation in it and never will; four is more than the two
// characters in the plan can put on screen, and the pool being full drops the
// new one rather than growing.
const MaxProjectiles = 4

// Projectile is one thing in flight. Every field is 4 bytes, like everything
// else in the state.
type Projectile struct {
	// Active is 0 for a free slot. Slots are never reordered or compacted —
	// compaction would make the same match produce different slot assignments
	// depending on when a rollback happened.
	Active int32

	Owner int32
	// Move indexes the owner's move list. Damage, stun and the box live there
	// and are read through it: copying them into the projectile would give one
	// number two homes and let them disagree after a balance change.
	Move int32

	X, Y Fix
	VX   Fix
	Life int32
}

// spec is the projectile data of the move that fired it.
func (s *GameState) spec(pr *Projectile) *ProjectileSpec {
	c := CharacterAt(s.Players[pr.Owner].Char)
	if pr.Move < 0 || pr.Move >= c.NumMoves {
		return nil
	}
	return &c.Moves[pr.Move].Proj
}

// ProjectileBox is the world-space hitbox of slot i, or an empty box if the
// slot is free. Facing comes from the direction of travel, not from the owner:
// the owner may have turned around since, and the fireball did not.
func (s *GameState) ProjectileBox(i int) Box {
	pr := &s.Projectiles[i]
	if pr.Active == 0 {
		return Box{}
	}
	sp := s.spec(pr)
	if sp == nil {
		return Box{}
	}
	facing := int32(1)
	if pr.VX < 0 {
		facing = -1
	}
	return sp.Box.World(pr.X, pr.Y, facing)
}

// advanceProjectiles is step 5: move what is in flight, expire it, then spawn
// what this frame's moves produce — in that order, so a fireball does not
// travel on the frame it appears.
func (s *GameState) advanceProjectiles() {
	for i := range s.Projectiles {
		pr := &s.Projectiles[i]
		if pr.Active == 0 {
			continue
		}

		pr.X += pr.VX
		pr.Life--

		// Off the end of the stage or out of life. There is no wall to hit:
		// the stage bound is where the screen ends, and a fireball that has
		// left it is gone whatever it does next.
		if pr.Life <= 0 || pr.X < -StageHalfWidth || pr.X > StageHalfWidth {
			pr.Active = 0
		}
	}

	for i := range s.Players {
		p := &s.Players[i]
		mv := p.move()
		// The spawn frame is the move's first active frame, which is data the
		// move already carries. A separate "spawn on frame N" field would be a
		// second place for the same timing to be wrong in.
		if mv == nil || !mv.Proj.Exists() || p.StateFrame != mv.Startup {
			continue
		}
		s.spawn(int32(i), p.MoveIndex, mv)
	}
}

// spawn puts a projectile in the first free slot. A full pool drops it — the
// alternative is evicting someone's live fireball, which is worse to play
// against than a fireball that did not come out.
func (s *GameState) spawn(owner, move int32, mv *Move) {
	p := &s.Players[owner]

	for i := range s.Projectiles {
		pr := &s.Projectiles[i]
		if pr.Active != 0 {
			continue
		}
		*pr = Projectile{
			Active: 1,
			Owner:  owner,
			Move:   move,
			X:      p.X + mv.Proj.SpawnX.Mul(FromInt(int(p.Facing))),
			Y:      p.Y + mv.Proj.SpawnY,
			VX:     mv.Proj.Speed.Mul(FromInt(int(p.Facing))),
			Life:   mv.Proj.Life,
		}
		return
	}
}

// resolveProjectiles is the projectile half of steps 6 and 7. It runs after the
// players' own hits, and slot by slot in index order — the order is arbitrary
// but it is the same arbitrary order on both machines, which is the only
// property that matters.
//
// A projectile that connects is spent, on block as much as on hit. Multi-hit
// projectiles are an EX property and belong with the Drive system.
func (s *GameState) resolveProjectiles(in [2]uint16) {
	var hurts [MaxBoxes]Box

	for i := range s.Projectiles {
		pr := &s.Projectiles[i]
		if pr.Active == 0 {
			continue
		}

		defender := int(1 - pr.Owner)
		box := s.ProjectileBox(i)
		if box.Empty() {
			continue
		}

		n := s.Hurtboxes(defender, &hurts)
		for d := int32(0); d < n; d++ {
			if !box.Overlaps(hurts[d]) {
				continue
			}
			c := CharacterAt(s.Players[pr.Owner].Char)
			mv := &c.Moves[pr.Move]
			// A fireball obeys the juggle limit like anything else, and obeys
			// it *before* it is spent: a projectile that has run out of juggles
			// passes through and stays in flight, the same whiff the move
			// itself would have.
			if s.juggled(defender, mv) {
				break
			}
			s.applyHit(int(pr.Owner), defender, mv, in[defender])
			pr.Active = 0
			break
		}
	}
}
