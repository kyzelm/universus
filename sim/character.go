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
	MaxMoves      = 8
	MaxKeyframes  = 8
	MaxBoxes      = 4
)

// Attack levels: what must be done to block it.
const (
	LevelMid = iota
	LevelHigh
	LevelLow
)

// Stances a move can be performed from.
const (
	StanceStand = iota
	StanceCrouch
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

	NumKeys int32
	Keys    [MaxKeyframes]Keyframe
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
