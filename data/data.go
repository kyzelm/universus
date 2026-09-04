// Package data owns the character and balance JSON and converts it into the
// sim's fixed-point structs.
//
// The files are embedded at build time, not fetched at runtime, and that is a
// determinism decision rather than a packaging one: **character data is part of
// the simulation's identity.** Two clients that could load different numbers
// would desync, and the desync would surface three rounds into a match with no
// visible cause. Embedding puts the balance data inside the binary hash, so a
// mismatch is caught by the handshake before the match starts.
//
// This package imports sim; sim never imports this one. The sim does no I/O and
// has no dependencies — loading is the caller's job, done once at startup.
package data

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"universus/sim"
)

//go:embed characters/*.json balance.json
var files embed.FS

// balanceFile is the global tunables. Not a character, but part of the same
// identity for the same reason: two clients with different Drive costs run
// different simulations from identical inputs, so it is embedded and it is
// covered by Version.
const balanceFile = "balance.json"

// Load reads, validates and converts the embedded roster.
//
// Order is by filename, so the roster indices are the same on every machine —
// a roster that ordered differently would have the two clients running
// different characters from the same selection.
func Load() ([]sim.Character, error) {
	names, err := filenames()
	if err != nil {
		return nil, err
	}

	out := make([]sim.Character, 0, len(names))
	for _, name := range names {
		raw, err := files.ReadFile(name)
		if err != nil {
			return nil, err
		}

		var jc jsonCharacter
		dec := json.NewDecoder(bytes.NewReader(raw))
		// Keeps decimals as their literal text so they can be converted with
		// integer arithmetic. Nothing here ever becomes a float.
		dec.UseNumber()
		if err := dec.Decode(&jc); err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}

		c, err := jc.convert()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		out = append(out, c)
	}

	if len(out) == 0 {
		return nil, fmt.Errorf("no character files embedded")
	}
	return out, nil
}

// LoadBalance reads, validates and converts the embedded tunables.
func LoadBalance() (sim.Balance, error) {
	var b sim.Balance

	raw, err := files.ReadFile(balanceFile)
	if err != nil {
		return b, err
	}

	var jb jsonBalance
	if err := json.Unmarshal(raw, &jb); err != nil {
		return b, fmt.Errorf("%s: %w", balanceFile, err)
	}

	b, err = jb.convert()
	if err != nil {
		return b, fmt.Errorf("%s: %w", balanceFile, err)
	}
	return b, nil
}

// Version is a hash of the raw embedded bytes — the value the match handshake
// compares. It covers the files as authored, before conversion, so any edit to
// any character changes it.
//
// FNV-1a, same as the state checksum: this is a mismatch detector, not a
// security hash.
func Version() (uint32, error) {
	names, err := filenames()
	if err != nil {
		return 0, err
	}

	h := uint32(2166136261)
	// The balance file is hashed with the roster: a client that tuned the block
	// cost is as desynced as one that tuned a hitbox.
	for _, name := range append(names, balanceFile) {
		raw, err := files.ReadFile(name)
		if err != nil {
			return 0, err
		}
		// The name is hashed too, so renaming a file is a version change.
		for _, b := range append([]byte(name), raw...) {
			h = (h ^ uint32(b)) * 16777619
		}
	}
	return h, nil
}

// filenames returns the embedded files in a fixed order. embed.FS.ReadDir is
// already sorted, but sorting explicitly says the order is load-bearing.
func filenames() ([]string, error) {
	entries, err := files.ReadDir("characters")
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, "characters/"+e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

// ---- JSON shapes ----------------------------------------------------------

type jsonCharacter struct {
	ID   int    `json:"id"`
	Name string `json:"name"`

	Health int `json:"health"`

	WalkForward json.Number `json:"walkForward"`
	WalkBack    json.Number `json:"walkBack"`

	DashDistance   json.Number `json:"dashDistance"`
	DashFrames     int         `json:"dashFrames"`
	BackdashFrames int         `json:"backdashFrames"`

	JumpVelocity  json.Number `json:"jumpVelocity"`
	JumpForwardVX json.Number `json:"jumpForwardVX"`
	Gravity       json.Number `json:"gravity"`
	PreJumpFrames int         `json:"preJumpFrames"`

	Pushbox    []json.Number `json:"pushbox"`
	StandHurt  []json.Number `json:"standHurt"`
	CrouchHurt []json.Number `json:"crouchHurt"`
	AirHurt    []json.Number `json:"airHurt"`

	Moves []jsonMove `json:"moves"`
}

// jsonBalance groups by resource, because that is how the design note reads and
// how the values are tuned. All integers: a resource is never a float.
type jsonBalance struct {
	Drive struct {
		Regen            int `json:"regen"`
		RegenWalkForward int `json:"regenWalkForward"`
		RegenBurnout     int `json:"regenBurnout"`
		BlockCost        int `json:"blockCost"`
		BurnoutBlockstun int `json:"burnoutBlockstun"`
		BurnoutChip      int `json:"burnoutChipPercent"`
	} `json:"drive"`

	Damage struct {
		ComboScale        []int `json:"comboScale"`
		StarterLight      int   `json:"starterLight"`
		StarterMedium     int   `json:"starterMedium"`
		StarterHeavy      int   `json:"starterHeavy"`
		MinDamagePercent  int   `json:"minDamagePercent"`
		CounterHitPercent int   `json:"counterHitPercent"`
		CounterHitstun    int   `json:"counterHitstun"`
		PunishHitstun     int   `json:"punishHitstun"`
	} `json:"damage"`

	Super struct {
		DealtPercent int `json:"dealtPercent"`
		TakenPercent int `json:"takenPercent"`
		OnSpecial    int `json:"onSpecial"`
	} `json:"super"`
}

func (jb *jsonBalance) convert() (sim.Balance, error) {
	var b sim.Balance

	for _, f := range []struct {
		name string
		n    int
		dst  *int32
	}{
		{"drive.regen", jb.Drive.Regen, &b.DriveRegen},
		{"drive.regenWalkForward", jb.Drive.RegenWalkForward, &b.DriveRegenWalkF},
		{"drive.regenBurnout", jb.Drive.RegenBurnout, &b.DriveRegenBurnout},
		{"drive.blockCost", jb.Drive.BlockCost, &b.DriveBlockCost},
		{"drive.burnoutBlockstun", jb.Drive.BurnoutBlockstun, &b.BurnoutBlockstun},
		{"drive.burnoutChipPercent", jb.Drive.BurnoutChip, &b.BurnoutChipPercent},
		{"damage.starterLight", jb.Damage.StarterLight, &b.StarterLight},
		{"damage.starterMedium", jb.Damage.StarterMedium, &b.StarterMedium},
		{"damage.starterHeavy", jb.Damage.StarterHeavy, &b.StarterHeavy},
		{"damage.minDamagePercent", jb.Damage.MinDamagePercent, &b.MinDamagePercent},
		{"damage.counterHitPercent", jb.Damage.CounterHitPercent, &b.CounterHitPercent},
		{"damage.counterHitstun", jb.Damage.CounterHitstun, &b.CounterHitstun},
		{"damage.punishHitstun", jb.Damage.PunishHitstun, &b.PunishHitstun},
		{"super.dealtPercent", jb.Super.DealtPercent, &b.SuperDealtPercent},
		{"super.takenPercent", jb.Super.TakenPercent, &b.SuperTakenPercent},
		{"super.onSpecial", jb.Super.OnSpecial, &b.SuperOnSpecial},
	} {
		if f.n < 0 {
			return b, fmt.Errorf("%s must not be negative, got %d", f.name, f.n)
		}
		*f.dst = int32(f.n)
	}

	// Chip is a fraction of the move's damage, so a value over 100 makes
	// blocking a special worse than eating it.
	if b.BurnoutChipPercent > 100 {
		return b, fmt.Errorf("drive.burnoutChipPercent is %d, want 0..100", b.BurnoutChipPercent)
	}
	if b.DriveBlockCost <= 0 {
		return b, fmt.Errorf("drive.blockCost must be positive, or blocking is free")
	}
	// Walking in is meant to be the faster rate. If it is not, the gauge
	// rewards backing off, which is the opposite of the design.
	if b.DriveRegenWalkF < b.DriveRegen {
		return b, fmt.Errorf("drive.regenWalkForward %d is below drive.regen %d",
			b.DriveRegenWalkF, b.DriveRegen)
	}

	// Coupled, like jumpVelocity against gravity: the refill rate is what sets
	// how long Burnout lasts, and neither field says so on its own. The design
	// note asks for ~9 seconds; anything from 1 to 30 is a game, and zero is a
	// player who never comes out of Burnout at all.
	if b.DriveRegenBurnout <= 0 {
		return b, fmt.Errorf("drive.regenBurnout must be positive, or Burnout never ends")
	}
	if f := sim.DriveMax / b.DriveRegenBurnout; f < 60 || f > 1800 {
		return b, fmt.Errorf("drive.regenBurnout %d gives a %d-frame Burnout, want 60..1800",
			b.DriveRegenBurnout, f)
	}

	// The scaling table. Exactly one entry per step, because the last one is
	// the floor every hit past the table takes and a short table would silently
	// move that floor.
	if len(jb.Damage.ComboScale) != sim.ComboScaleSteps {
		return b, fmt.Errorf("damage.comboScale has %d entries, want %d",
			len(jb.Damage.ComboScale), sim.ComboScaleSteps)
	}
	prev := 101
	for i, v := range jb.Damage.ComboScale {
		if v <= 0 || v > 100 {
			return b, fmt.Errorf("damage.comboScale[%d] is %d, want 1..100", i, v)
		}
		// Scaling that goes back up is a transposed pair, and the symptom is a
		// combo that deals more on its sixth hit than its fifth.
		if v > prev {
			return b, fmt.Errorf("damage.comboScale[%d] is %d, above the %d before it", i, v, prev)
		}
		prev = v
		b.ComboScale[i] = int32(v)
	}

	for _, f := range []struct {
		name string
		v    int32
		lo   int32
		hi   int32
	}{
		{"damage.starterLight", b.StarterLight, 1, 100},
		{"damage.starterMedium", b.StarterMedium, 1, 100},
		{"damage.starterHeavy", b.StarterHeavy, 1, 100},
		{"damage.minDamagePercent", b.MinDamagePercent, 1, 100},
		// A counter hit that pays less than a normal one is the sign flipped.
		{"damage.counterHitPercent", b.CounterHitPercent, 100, 300},
	} {
		if f.v < f.lo || f.v > f.hi {
			return b, fmt.Errorf("%s is %d, want %d..%d", f.name, f.v, f.lo, f.hi)
		}
	}

	// The starters are an ordering, not three independent numbers: a light that
	// scales harder than a heavy inverts what starter scaling is for.
	if b.StarterLight > b.StarterMedium || b.StarterMedium > b.StarterHeavy {
		return b, fmt.Errorf("starter scaling is not ordered light <= medium <= heavy: %d, %d, %d",
			b.StarterLight, b.StarterMedium, b.StarterHeavy)
	}
	// A punish counter is the bigger reward of the two, by definition.
	if b.PunishHitstun < b.CounterHitstun {
		return b, fmt.Errorf("damage.punishHitstun %d is below damage.counterHitstun %d",
			b.PunishHitstun, b.CounterHitstun)
	}

	return b, nil
}

type jsonMove struct {
	ID    string `json:"id"`
	Input struct {
		Stance string `json:"stance"`
		Button string `json:"button"`
		Motion string `json:"motion"`
	} `json:"input"`

	Startup  int `json:"startup"`
	Active   int `json:"active"`
	Recovery int `json:"recovery"`

	Damage    int `json:"damage"`
	Hitstun   int `json:"hitstun"`
	Blockstun int `json:"blockstun"`
	Hitstop   int `json:"hitstop"`

	AttackLevel string `json:"attackLevel"`

	// Recovery owed on touchdown by a move that was still in the air when it
	// ended. Absent on every move that cannot end up there.
	Landing int `json:"landing"`

	// Invulnerable window, [start, end) in the move's own frames. Absent on
	// everything that is not a reversal.
	Invuln []int `json:"invuln"`

	// Categories this move may be cancelled into once it has connected.
	// Absent means it does not cancel, which is most of the list.
	Cancel []string `json:"cancel"`

	// Super level, 1 to 3. Absent on everything that is not one, which is the
	// whole roster bar three moves per character.
	Super int `json:"super"`

	// Velocity the move gives the character on its first frame, [vx, vy],
	// forward-relative. Absent for the moves that do not move anyone.
	Launch []json.Number `json:"launch"`

	Boxes []jsonKeyframe `json:"boxes"`

	// Absent for the moves that do not fire one, which is most of them.
	Projectile *jsonProjectile `json:"projectile"`
}

type jsonProjectile struct {
	Speed json.Number   `json:"speed"`
	Life  int           `json:"life"`
	Spawn []json.Number `json:"spawn"`
	Box   []json.Number `json:"box"`
}

type jsonKeyframe struct {
	Frame int             `json:"frame"`
	Hurt  [][]json.Number `json:"hurt"`
	Hit   [][]json.Number `json:"hit"`
}

var buttons = map[string]uint16{
	"LP": sim.InLP, "MP": sim.InMP, "HP": sim.InHP,
	"LK": sim.InLK, "MK": sim.InMK, "HK": sim.InHK,
}

var levels = map[string]int32{
	"mid": sim.LevelMid, "high": sim.LevelHigh, "low": sim.LevelLow,
	// An overhead is a high that must be blocked standing; the distinction is
	// startup, which is already in the frame data.
	"overhead": sim.LevelHigh,
}

var cancelCategories = map[string]uint16{
	"chain": sim.CancelChain, "special": sim.CancelSpecial,
	"super1": sim.CancelSuper1, "super2": sim.CancelSuper2, "super3": sim.CancelSuper3,
}

var stances = map[string]int32{
	"stand": sim.StanceStand, "crouch": sim.StanceCrouch, "air": sim.StanceAir,
}

// Motions a move can require. Absent means a normal, which is why the empty
// string is a key and not an error: most moves have no motion, and making them
// all write "none" would be noise in every entry.
var inputMotions = map[string]sim.Motion{
	"": sim.MotionNone, "qcf": sim.MotionQCF, "qcb": sim.MotionQCB, "dp": sim.MotionDP,
	"qcfx2": sim.MotionQCFx2, "qcbx2": sim.MotionQCBx2,
}

// ---- conversion and validation --------------------------------------------

// Validation is not optional. This data is authored by hand and by tooling and
// *will* contain mistakes; a silent bad value becomes a desync or an
// unwinnable matchup found weeks later. Every failure below names the field.
func (jc *jsonCharacter) convert() (sim.Character, error) {
	var c sim.Character
	var err error

	if jc.Health <= 0 {
		return c, fmt.Errorf("health must be positive, got %d", jc.Health)
	}
	c.Health = int32(jc.Health)

	for _, f := range []struct {
		name string
		src  json.Number
		dst  *sim.Fix
	}{
		{"walkForward", jc.WalkForward, &c.WalkForward},
		{"walkBack", jc.WalkBack, &c.WalkBack},
		{"dashDistance", jc.DashDistance, &c.DashDistance},
		{"jumpVelocity", jc.JumpVelocity, &c.JumpVelocity},
		{"jumpForwardVX", jc.JumpForwardVX, &c.JumpForwardVX},
		{"gravity", jc.Gravity, &c.Gravity},
	} {
		if *f.dst, err = parseFix(f.src); err != nil {
			return c, fmt.Errorf("%s: %w", f.name, err)
		}
	}

	if c.Gravity >= 0 {
		return c, fmt.Errorf("gravity must be negative (down), got %d", c.Gravity)
	}
	if c.JumpVelocity <= 0 {
		return c, fmt.Errorf("jumpVelocity must be positive, got %d", c.JumpVelocity)
	}

	// Coupled constants, checked against each other rather than individually.
	// A jump lasts about 2*v/|g| frames; the pair that produced a 533-frame
	// jump in an earlier draft of the design note passes every per-field check
	// and is still obviously wrong.
	if air := 2 * int64(c.JumpVelocity) / int64(-c.Gravity); air < 20 || air > 90 {
		return c, fmt.Errorf("jumpVelocity %d against gravity %d gives a %d-frame jump, want 20..90",
			c.JumpVelocity, c.Gravity, air)
	}

	for _, f := range []struct {
		name string
		n    int
		dst  *int32
	}{
		{"dashFrames", jc.DashFrames, &c.DashFrames},
		{"backdashFrames", jc.BackdashFrames, &c.BackdashFrames},
		{"preJumpFrames", jc.PreJumpFrames, &c.PreJumpFrames},
	} {
		if f.n <= 0 {
			return c, fmt.Errorf("%s must be positive, got %d", f.name, f.n)
		}
		*f.dst = int32(f.n)
	}

	for _, f := range []struct {
		name string
		src  []json.Number
		dst  *sim.Box
	}{
		{"pushbox", jc.Pushbox, &c.Pushbox},
		{"standHurt", jc.StandHurt, &c.StandHurt},
		{"crouchHurt", jc.CrouchHurt, &c.CrouchHurt},
		{"airHurt", jc.AirHurt, &c.AirHurt},
	} {
		if *f.dst, err = parseBox(f.src); err != nil {
			return c, fmt.Errorf("%s: %w", f.name, err)
		}
	}

	if len(jc.Moves) == 0 {
		return c, fmt.Errorf("character has no moves")
	}
	if len(jc.Moves) > sim.MaxMoves {
		return c, fmt.Errorf("%d moves, max %d", len(jc.Moves), sim.MaxMoves)
	}
	for i := range jc.Moves {
		m, err := jc.Moves[i].convert()
		if err != nil {
			return c, fmt.Errorf("move %q: %w", jc.Moves[i].ID, err)
		}
		c.Moves[i] = m
	}
	c.NumMoves = int32(len(jc.Moves))

	return c, nil
}

func (jm *jsonMove) convert() (sim.Move, error) {
	var m sim.Move

	if jm.Startup <= 0 {
		return m, fmt.Errorf("startup must be positive, got %d", jm.Startup)
	}
	if jm.Active <= 0 {
		return m, fmt.Errorf("active must be positive, got %d", jm.Active)
	}
	if jm.Recovery < 0 {
		return m, fmt.Errorf("recovery must not be negative, got %d", jm.Recovery)
	}
	if jm.Damage < 0 {
		return m, fmt.Errorf("damage must not be negative, got %d", jm.Damage)
	}
	if jm.Hitstun < 0 || jm.Blockstun < 0 || jm.Hitstop < 0 {
		return m, fmt.Errorf("hitstun/blockstun/hitstop must not be negative")
	}

	m.Startup = int32(jm.Startup)
	m.Active = int32(jm.Active)
	m.Recovery = int32(jm.Recovery)
	m.Damage = int32(jm.Damage)
	m.Hitstun = int32(jm.Hitstun)
	m.Blockstun = int32(jm.Blockstun)
	m.Hitstop = int32(jm.Hitstop)

	var ok bool
	if m.Button, ok = buttons[jm.Input.Button]; !ok {
		return m, fmt.Errorf("unknown button %q", jm.Input.Button)
	}
	if m.Stance, ok = stances[jm.Input.Stance]; !ok {
		return m, fmt.Errorf("unknown stance %q", jm.Input.Stance)
	}
	if m.Level, ok = levels[jm.AttackLevel]; !ok {
		return m, fmt.Errorf("unknown attackLevel %q", jm.AttackLevel)
	}
	if m.Motion, ok = inputMotions[jm.Input.Motion]; !ok {
		return m, fmt.Errorf("unknown motion %q", jm.Input.Motion)
	}

	if jm.Landing < 0 {
		return m, fmt.Errorf("landing must not be negative, got %d", jm.Landing)
	}
	m.Landing = int32(jm.Landing)

	if len(jm.Invuln) != 0 {
		if len(jm.Invuln) != 2 {
			return m, fmt.Errorf("invuln wants [start, end), got %d values", len(jm.Invuln))
		}
		start, end := jm.Invuln[0], jm.Invuln[1]
		// Half-open and non-empty: an authored window that contains no frames
		// is a move someone believes is invincible and is not.
		if start < 0 || end <= start {
			return m, fmt.Errorf("invuln [%d, %d) is empty or negative", start, end)
		}
		if int32(end) > m.Total() {
			return m, fmt.Errorf("invuln ends at frame %d, but the move is %d frames", end, m.Total())
		}
		m.InvulnStart, m.InvulnEnd = int32(start), int32(end)
	}

	if jm.Super < 0 || jm.Super > 3 {
		return m, fmt.Errorf("super must be 1, 2 or 3, got %d", jm.Super)
	}
	m.Super = int32(jm.Super)

	// A super with no motion is a super on a bare button press, which would
	// beat the normal on that button for the rest of the match. The typo is
	// silent otherwise: the move works, it is just never not selected.
	if m.Super > 0 && m.Motion == sim.MotionNone {
		return m, fmt.Errorf("super %d has no motion", m.Super)
	}

	for _, name := range jm.Cancel {
		bit, ok := cancelCategories[name]
		if !ok {
			return m, fmt.Errorf("unknown cancel category %q", name)
		}
		m.CancelInto |= bit
	}

	if len(jm.Boxes) == 0 {
		return m, fmt.Errorf("no box keyframes")
	}
	if len(jm.Boxes) > sim.MaxKeyframes {
		return m, fmt.Errorf("%d box keyframes, max %d", len(jm.Boxes), sim.MaxKeyframes)
	}

	prev := int32(-1)
	for i := range jm.Boxes {
		k, err := jm.Boxes[i].convert()
		if err != nil {
			return m, fmt.Errorf("box frame %d: %w", jm.Boxes[i].Frame, err)
		}
		// BoxesAt scans forward and stops at the first entry past the frame it
		// wants, so out-of-order keyframes would silently never apply.
		if k.Frame <= prev {
			return m, fmt.Errorf("box keyframes must be in ascending frame order, %d after %d", k.Frame, prev)
		}
		if k.Frame >= m.Total() {
			return m, fmt.Errorf("box keyframe at frame %d, but the move is %d frames", k.Frame, m.Total())
		}
		prev = k.Frame
		m.Keys[i] = k
	}
	m.NumKeys = int32(len(jm.Boxes))

	if len(jm.Launch) != 0 {
		if len(jm.Launch) != 2 {
			return m, fmt.Errorf("launch wants [vx, vy], got %d values", len(jm.Launch))
		}
		vx, err := parseFix(jm.Launch[0])
		if err != nil {
			return m, fmt.Errorf("launch vx: %w", err)
		}
		vy, err := parseFix(jm.Launch[1])
		if err != nil {
			return m, fmt.Errorf("launch vy: %w", err)
		}
		m.LaunchVX, m.LaunchVY = vx, vy
	}

	if jm.Projectile != nil {
		p, err := jm.Projectile.convert()
		if err != nil {
			return m, fmt.Errorf("projectile: %w", err)
		}
		m.Proj = p
	}

	// A hitbox outside the active window can never connect, which means the
	// frame data and the boxes disagree and one of them is a typo.
	for i := int32(0); i < m.NumKeys; i++ {
		k := &m.Keys[i]
		if k.NumHit == 0 {
			continue
		}
		if k.Frame < m.Startup || k.Frame >= m.Startup+m.Active {
			return m, fmt.Errorf("hitbox at frame %d is outside the active window %d..%d",
				k.Frame, m.Startup, m.Startup+m.Active-1)
		}
	}

	return m, nil
}

func (jp *jsonProjectile) convert() (sim.ProjectileSpec, error) {
	var p sim.ProjectileSpec
	var err error

	if p.Speed, err = parseFix(jp.Speed); err != nil {
		return p, fmt.Errorf("speed: %w", err)
	}
	if p.Speed <= 0 {
		return p, fmt.Errorf("speed must be positive, got %s", jp.Speed)
	}
	// Life is what marks a move as firing a projectile at all, so zero here is
	// not "a very short fireball", it is a move that silently does nothing.
	if jp.Life <= 0 {
		return p, fmt.Errorf("life must be positive, got %d", jp.Life)
	}
	p.Life = int32(jp.Life)

	if len(jp.Spawn) != 2 {
		return p, fmt.Errorf("spawn wants [x, y], got %d values", len(jp.Spawn))
	}
	if p.SpawnX, err = parseFix(jp.Spawn[0]); err != nil {
		return p, fmt.Errorf("spawn x: %w", err)
	}
	if p.SpawnY, err = parseFix(jp.Spawn[1]); err != nil {
		return p, fmt.Errorf("spawn y: %w", err)
	}

	if p.Box, err = parseBox(jp.Box); err != nil {
		return p, fmt.Errorf("box: %w", err)
	}
	return p, nil
}

func (jk *jsonKeyframe) convert() (sim.Keyframe, error) {
	var k sim.Keyframe

	if jk.Frame < 0 {
		return k, fmt.Errorf("negative frame")
	}
	k.Frame = int32(jk.Frame)

	if len(jk.Hurt) > sim.MaxBoxes || len(jk.Hit) > sim.MaxBoxes {
		return k, fmt.Errorf("more than %d boxes", sim.MaxBoxes)
	}

	for i, raw := range jk.Hurt {
		b, err := parseBox(raw)
		if err != nil {
			return k, fmt.Errorf("hurt[%d]: %w", i, err)
		}
		k.Hurt[i] = b
	}
	k.NumHurt = int32(len(jk.Hurt))

	for i, raw := range jk.Hit {
		b, err := parseBox(raw)
		if err != nil {
			return k, fmt.Errorf("hit[%d]: %w", i, err)
		}
		k.Hit[i] = b
	}
	k.NumHit = int32(len(jk.Hit))

	return k, nil
}

// parseBox reads [x, y, width, height].
func parseBox(raw []json.Number) (sim.Box, error) {
	var b sim.Box
	if len(raw) != 4 {
		return b, fmt.Errorf("want [x, y, w, h], got %d values", len(raw))
	}

	var err error
	dst := [...]*sim.Fix{&b.X, &b.Y, &b.W, &b.H}
	for i, n := range raw {
		if *dst[i], err = parseFix(n); err != nil {
			return b, err
		}
	}

	// A zero-size box silently never collides, which reads as a move that
	// mysteriously does not work rather than as the typo it is.
	if b.W <= 0 || b.H <= 0 {
		return b, fmt.Errorf("width and height must be positive, got %d x %d", b.W, b.H)
	}
	return b, nil
}

// parseFix converts a decimal literal to 16.16 using integer arithmetic only.
//
// Not strconv.ParseFloat then multiply. Correctly-rounded float parsing would
// in fact be deterministic here, but "we banned floats from the simulation and
// then produced every gameplay constant with one" is a bad answer to an obvious
// question, and the integer path has no edge cases to argue about.
func parseFix(n json.Number) (sim.Fix, error) {
	s := n.String()
	if s == "" {
		return 0, fmt.Errorf("missing value")
	}

	neg := false
	switch s[0] {
	case '-':
		neg, s = true, s[1:]
	case '+':
		s = s[1:]
	}

	whole, frac, hasFrac := strings.Cut(s, ".")
	if whole == "" {
		whole = "0"
	}

	// strconv.ParseInt accepts its own sign, so "--1" would strip one here and
	// the other there and come out positive. Digits only, past this point.
	if !digitsOnly(whole) || (hasFrac && !digitsOnly(frac)) {
		return 0, fmt.Errorf("bad number %q", n.String())
	}

	units, err := strconv.ParseInt(whole, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("bad number %q", n.String())
	}
	// Bounded before the shift below, which would otherwise overflow int64 and
	// land on a plausible-looking value rather than failing.
	if units > 1<<20 || units < -(1<<20) {
		return 0, fmt.Errorf("%q is out of range for 16.16", n.String())
	}

	var scaled int64
	if hasFrac {
		if frac == "" {
			return 0, fmt.Errorf("bad number %q", n.String())
		}
		// More digits than this cannot survive 1/65536 of a unit anyway, and
		// the intermediate below has to stay inside int64.
		if len(frac) > 9 {
			frac = frac[:9]
		}
		digits, err := strconv.ParseInt(frac, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("bad number %q", n.String())
		}
		den := int64(1)
		for range len(frac) {
			den *= 10
		}
		// Round half away from zero, in integers: (2*num/den + 1) / 2.
		scaled = ((digits<<(sim.FracBits+1))/den + 1) / 2
	}

	v := units<<sim.FracBits + scaled
	if neg {
		v = -v
	}
	// 16.16 holds ±32768. A value past that wraps into a plausible-looking
	// number of the wrong sign, which is the worst possible failure mode.
	if v > (1<<31)-1 || v < -(1<<31) {
		return 0, fmt.Errorf("%q is out of range for 16.16", n.String())
	}
	return sim.Fix(v), nil
}

func digitsOnly(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
