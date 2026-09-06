package sim

// Character data: frame data, damage, boxes and speeds. **No gameplay value is
// hardcoded in sim code.** Adding a character must never require touching this
// package, which is the property that makes "two now, more later" a data-entry
// job instead of a rewrite.
//
// This is not GameState. It is immutable for the whole match, identical on both
// machines, and it does not roll back — so it lives beside the state rather
// than inside it, and the 4-byte layout rule that governs GameState does not
// apply here.
//
// It is still part of the simulation's *identity*: two clients running
// different numbers desync. The data is embedded in the binary at build time
// (see package data) and its hash goes in the match handshake.

// Fixed caps. Static data, so generous is free — but bounded, because a loader
// that can grow these is a loader that can produce two different sims.
const (
	MaxCharacters = 4
	MaxMoves      = 32
	MaxKeyframes  = 8
	MaxBoxes      = 4
)

// Attack levels: what must be done to block it.
const (
	LevelMid = iota
	LevelHigh
	LevelLow
)

// Cancel categories — the bitmask a move carries saying what it may be
// cancelled into once it has connected (03 Game Design/Combat System.md, D34).
// Drive takes the next bit when that system exists.
//
// The three super levels are separate categories rather than one, because the
// design tiers them by source: a level 1 comes out of cancelable normals only,
// a level 2 out of those and specials, and a level 3 out of anything at all
// including the heavies that cancel into nothing else. That tiering is data —
// each source move names the levels it feeds — and it costs three bits.
const (
	CancelChain   uint16 = 1 << 0 // another normal
	CancelSpecial uint16 = 1 << 1
	CancelSuper1  uint16 = 1 << 2
	CancelSuper2  uint16 = 1 << 3
	CancelSuper3  uint16 = 1 << 4
)

// superCancel is the category of a super at the given level. Index 0 is unused:
// level 0 is not a super.
var superCancel = [4]uint16{0, CancelSuper1, CancelSuper2, CancelSuper3}

// Stances a move can be performed from.
const (
	StanceStand = iota
	StanceCrouch
	StanceAir
)

// Box is an axis-aligned rectangle in character-local space: origin at the
// feet, positive x forward, positive y up. Facing is applied when it is placed
// in the world, so data is authored once and mirrors for free.
//
// AABBs and nothing else — no rotation, no polygons, no physics engine.
// Fighting games use them precisely because they are exact, cheap and
// reproducible, which is worth a sentence in the thesis.
type Box struct {
	X, Y, W, H Fix
}

// Empty reports a box that should not participate in collision. Zero-size
// boxes are rejected at load, so this only catches the absent-box case.
func (b Box) Empty() bool { return b.W <= 0 || b.H <= 0 }

// World places a local box at a world position for a given facing.
func (b Box) World(x, y Fix, facing int32) Box {
	if facing < 0 {
		// Forward is -x, so the box reflects about the character's own origin.
		return Box{X: x - b.X - b.W, Y: y + b.Y, W: b.W, H: b.H}
	}
	return Box{X: x + b.X, Y: y + b.Y, W: b.W, H: b.H}
}

// Overlaps is rectangle intersection on integers. Touching edges do not count:
// a box ending exactly where another begins is a miss, which keeps a hit and a
// whiff from depending on the last bit of a position.
func (b Box) Overlaps(o Box) bool {
	if b.Empty() || o.Empty() {
		return false
	}
	return b.X < o.X+o.W && o.X < b.X+b.W &&
		b.Y < o.Y+o.H && o.Y < b.Y+b.H
}

// Keyframe is the box set from its frame until the next keyframe. Sparse by
// design: an entry exists only where boxes change, and the previous entry
// persists until overridden.
type Keyframe struct {
	Frame   int32
	NumHurt int32
	NumHit  int32
	Hurt    [MaxBoxes]Box
	Hit     [MaxBoxes]Box
}

// ProjectileSpec is the projectile a move fires, if it fires one. Static data,
// like the rest of the character: the thing in flight is state, its numbers are
// not.
type ProjectileSpec struct {
	// Speed is forward units per frame; Life is how many frames it lives if it
	// hits nothing. Together they are the range.
	Speed Fix
	Life  int32

	// Spawn is where it appears, relative to the owner's origin and mirrored by
	// their facing. Box is its hitbox, relative to the projectile itself.
	SpawnX, SpawnY Fix
	Box            Box
}

// Exists reports whether the move fires a projectile at all. Life is the marker
// because a projectile with no life is not one.
func (p *ProjectileSpec) Exists() bool { return p.Life > 0 }

// Move is an attack: three integers and box data, per the design note.
type Move struct {
	Startup  int32
	Active   int32
	Recovery int32

	Damage    int32
	Hitstun   int32
	Blockstun int32
	Hitstop   int32

	Level  int32
	Stance int32
	Button uint16

	// Motion is the directional sequence the move requires, or MotionNone for a
	// normal. A move with one is a special: it ignores Stance (see moveFor),
	// because the motion already identifies it and the stance the button
	// happens to land on is not part of what the player asked for.
	Motion Motion

	// LaunchVX and LaunchVY are the velocity the move gives the character on
	// its first frame, forward-relative. An uppercut sets a vertical one and
	// gravity supplies the arc; an advancing normal sets a horizontal one.
	//
	// ponytail: one impulse per move, not per keyframe. A move that needs to
	// change velocity partway is a keyframe field away, and none does yet.
	LaunchVX, LaunchVY Fix

	// KnockbackVX and KnockbackVY are the velocity the move gives the
	// *defender* on a clean hit, directed away from the attacker. A vertical
	// component launches them and gravity supplies the arc; a purely
	// horizontal one is ordinary pushback, which is what spaces a blockstring
	// out and what the corner reverses.
	//
	// Zero means the balance file's default — every move that is not a
	// launcher wants the same push, and authoring it 24 times would be 24
	// chances to author it differently by accident. See Knockback.
	KnockbackVX, KnockbackVY Fix

	// JuggleLimit is the juggle count at or above which this move refuses to
	// connect with an airborne defender: the knob that designates a move a
	// combo ender. Zero means the balance file's default.
	JuggleLimit int32

	// Landing is the recovery owed on touchdown by a move that was still in
	// the air when it ended. Without it an air normal or an uppercut that runs
	// out above the ground makes the character actionable the instant they
	// touch it, which is a free reversal and a free jump-in.
	Landing int32

	// InvulnStart and InvulnEnd bound the frames on which the move has no
	// hurtboxes at all, in the move's own frames and half-open: [start, end).
	// Equal means none, which is most moves.
	//
	// ponytail: one window, whole-body. Strike-only and throw-only invuln are
	// different windows on the same move in a full game; there are no throws
	// yet, and a second pair of fields with nothing to distinguish them from
	// the first is a guess about a system that does not exist.
	InvulnStart, InvulnEnd int32

	// Knockdown marks a move that puts the defender on the floor when its
	// hitstun runs out — a sweep, a throw. A flag and not a duration: the
	// design note fixes one wakeup timing for every knockdown in the game, so
	// what varies per move is whether it knocks down at all.
	Knockdown int32

	// Throw marks a throw: unblockable, refused against anyone airborne or in
	// stun, and escapable by teching (03 Game Design/Movement and Defense.md).
	// A flag rather than a level, because a throw is one kind of thing and the
	// numbers that vary between throws are the ones every move already has.
	Throw int32

	// Super is the move's super level, 1 to 3, or 0 for everything else. It is
	// both the identity and the price: a level N super costs N bars, which is
	// the design's own table read straight down (03 Game Design/Resource
	// System.md). Split the two apart when a character wants a level 2 that
	// costs 3, and not before.
	Super int32

	// CancelInto is the set of categories this move may be cancelled into once
	// it has connected — zero for the moves that do not cancel, which is the
	// default and the majority.
	CancelInto uint16

	NumKeys int32
	Keys    [MaxKeyframes]Keyframe

	// Proj is the projectile this move fires, if any. A fireball's damage and
	// stun stay here, on the move — the projectile in flight reads them back
	// through its move index.
	Proj ProjectileSpec
}

// IsThrow reports a throw. Named rather than compared inline: the flag is read
// in five places and "mv.Throw != 0" reads like an amount of throw.
func (m *Move) IsThrow() bool { return m.Throw != 0 }

// KnocksDown reports a move that ends with the defender on the ground.
func (m *Move) KnocksDown() bool { return m.Knockdown != 0 }

// SuperCost is what the move costs to perform, in resource units. Zero for
// everything that is not a super, which is everything the meter does not gate.
func (m *Move) SuperCost() int32 { return m.Super * BarUnits }

// Category is the cancel category the move *is*, as opposed to the ones it
// cancels into. Derived rather than authored: a super declares its level, a
// motion makes a move a special, and the absence of both makes it a normal — so
// nothing has to say twice what it already is, and the two cannot disagree.
func (m *Move) Category() uint16 {
	if m.Super > 0 && m.Super < int32(len(superCancel)) {
		return superCancel[m.Super]
	}
	if m.Motion != MotionNone {
		return CancelSpecial
	}
	return CancelChain
}

// Launches reports whether the move sets its own velocity. A move that does
// not is content to be carried by whatever the character was already doing.
func (m *Move) Launches() bool { return m.LaunchVX != 0 || m.LaunchVY != 0 }

// Invulnerable reports whether the move is invulnerable on the given frame of
// itself. A move with no window answers false for every frame, since the empty
// half-open range contains nothing.
func (m *Move) Invulnerable(frame int32) bool {
	return frame >= m.InvulnStart && frame < m.InvulnEnd
}

// Total is the move's full duration in frames.
func (m *Move) Total() int32 { return m.Startup + m.Active + m.Recovery }

// Advantage values are computed, never authored. A hand-entered advantage
// drifts out of sync with the frame data the moment anything is tuned.

// OnHit is the frame advantage when the move connects on its first active frame.
func (m *Move) OnHit() int32 { return m.Hitstun - (m.Active - 1 + m.Recovery) }

// OnBlock is the same for a blocked hit.
func (m *Move) OnBlock() int32 { return m.Blockstun - (m.Active - 1 + m.Recovery) }

// BoxesAt returns the box set in effect on the given frame of the move.
// Scanning forward over at most MaxKeyframes entries, in index order — no map
// iteration, nothing that could order differently on another machine.
func (m *Move) BoxesAt(frame int32) *Keyframe {
	var cur *Keyframe
	for i := int32(0); i < m.NumKeys; i++ {
		if m.Keys[i].Frame > frame {
			break
		}
		cur = &m.Keys[i]
	}
	return cur
}

// Character is one fighter's complete data.
type Character struct {
	Health int32

	WalkForward Fix
	WalkBack    Fix

	DashDistance   Fix
	DashFrames     int32
	BackdashFrames int32

	JumpVelocity  Fix
	JumpForwardVX Fix
	Gravity       Fix
	PreJumpFrames int32

	Pushbox    Box
	StandHurt  Box
	CrouchHurt Box
	AirHurt    Box

	NumMoves int32
	Moves    [MaxMoves]Move
}

// The loaded roster. Package-level and mutable-once rather than a GameState
// field, because it is not gameplay state and must not roll back.
//
// Two clients with different rosters produce different simulations from
// identical inputs, and nothing in the sim can detect that — which is exactly
// why the handshake compares the data hash before the match starts, and why
// this is set once at startup and never again.
var (
	characters    [MaxCharacters]Character
	numCharacters int32

	// Returned for an out-of-range index. A package var rather than a fresh
	// &Character{}, which would allocate on every frame that asked.
	zeroCharacter Character
)

// LoadCharacters installs the roster. Extra entries beyond MaxCharacters are
// refused rather than truncated: silently dropping a character is a desync
// between a client that has it and one that does not.
func LoadCharacters(cs []Character) bool {
	if len(cs) == 0 || len(cs) > MaxCharacters {
		return false
	}
	characters = [MaxCharacters]Character{}
	copy(characters[:], cs)
	numCharacters = int32(len(cs))
	return true
}

// CharacterAt returns a roster entry, or the zero character for an out-of-range
// index. The zero character cannot move or attack, which is a visible failure
// rather than a crash inside a rollback replay.
func CharacterAt(i int32) *Character {
	if i < 0 || i >= numCharacters {
		return &zeroCharacter
	}
	return &characters[i]
}

// NumCharacters is how many are loaded.
func NumCharacters() int32 { return numCharacters }
