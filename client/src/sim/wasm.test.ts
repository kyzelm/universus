import {readFileSync} from 'node:fs'
import {beforeAll, expect, test} from 'vitest'
import '../../public/wasm_exec.js'
import {
  advance,
  BAR_UNITS,
  DRIVE_BARS,
  EVENT_HIT,
  eventsAt,
  initSim,
  readSnapshot,
  reset,
  SUPER_BARS,
} from './wasm'

// The Go build and the TypeScript reader agree on a byte layout or they do
// not. This runs the real module off disk and checks that they do.
beforeAll(async () => {
  // Relative to the vitest root (client/); import.meta.url is an http URL here.
  const wasm = readFileSync('public/main.wasm')
  await initSim(wasm)
})

// Input bits, mirroring sim/state.go.
const UP = 1 << 0
const DOWN = 1 << 1
const LEFT = 1 << 2
const RIGHT = 1 << 3
const LP = 1 << 4

/** Pre-jump frames, from the shipped character data. */
const PRE_JUMP = 4

test('frame 0 is readable before the first advance', () => {
  reset()
  const s = readSnapshot()
  expect(s.frame).toBe(0)
  expect(s.camX).toBe(0)
  expect(s.players.map((p) => [p.x, p.y, p.facing])).toEqual([
    [-60, 0, 1],
    [60, 0, -1],
  ])
  // Health comes from the embedded character file; a zero here means the
  // roster did not load inside the WASM binary.
  expect(s.players[0].health).toBeGreaterThan(0)
})

// The gauges are read from the same block as health and one field further
// along. A wrong offset here reads the pushbox as a resource, which draws a
// full Drive gauge for a player who is in Burnout.
test('the resource gauges cross the boundary', () => {
  reset()
  const start = readSnapshot()

  expect(start.players.map((p) => p.drive)).toEqual([
    DRIVE_BARS * BAR_UNITS,
    DRIVE_BARS * BAR_UNITS,
  ])
  expect(start.players.map((p) => [p.super, p.burnout])).toEqual([
    [0, 0],
    [0, 0],
  ])

  // Same walk-in as the hit test: they start out of range of everything, and
  // holding away walks the defender back out of it again.
  const walkIn = () => {
    for (let i = 0; i < 60; i++) advance(RIGHT, LEFT)
  }

  // Blocking spends Drive. Player 1 faces left, so RIGHT is holding away.
  walkIn()
  for (let i = 0; i < 40; i++) advance(i === 0 ? LP : 0, RIGHT)
  const blocked = readSnapshot()
  expect(blocked.players[1].drive).toBeLessThan(DRIVE_BARS * BAR_UNITS)
  expect(blocked.players[1].health).toBe(start.players[1].health)

  // Landing one builds Super for both of them, at different rates. Stop on the
  // frame it connects: the combo readout below only exists while the defender
  // is still in hitstun, which is the whole meaning of a combo.
  walkIn()
  const before = readSnapshot().players[1].health
  for (let i = 0; i < 40; i++) {
    advance(i === 0 ? LP : 0, 0)
    if (readSnapshot().players[1].health < before) break
  }
  const hit = readSnapshot()
  expect(hit.players[0].super).toBeGreaterThan(0)
  expect(hit.players[1].super).toBeGreaterThan(0)
  expect(hit.players[0].super).toBeGreaterThan(hit.players[1].super)
  expect(hit.players[0].super).toBeLessThanOrEqual(SUPER_BARS * BAR_UNITS)

  // The combo readout is read from two fields further along the same block, so
  // a wrong offset here would draw the pushbox width as a hit count.
  expect(hit.players[1].combo).toBe(1)
  expect(hit.players[1].counter).toBe(0) // an idle defender is no counter hit
  expect(hit.players[0].combo).toBe(0)
})

// The overlay is only worth having if it shows the boxes the sim collides.
test('boxes cross the boundary and follow the frame data', () => {
  reset()
  let s = readSnapshot()
  expect(s.players[0].hurtboxes.length).toBe(1)
  expect(s.players[0].hitboxes).toEqual([])
  expect(s.players[0].pushbox.w).toBeGreaterThan(0)

  // The jab is 4 startup, 3 active: a hitbox appears and then goes away.
  advance(LP, 0)
  let sawHitbox = false
  for (let i = 0; i < 13; i++) {
    advance(0, 0)
    s = readSnapshot()
    if (s.players[0].hitboxes.length > 0) sawHitbox = true
  }
  expect(sawHitbox).toBe(true)
  expect(readSnapshot().players[0].hitboxes).toEqual([])
})

test('a connecting hit costs health and freezes both fighters', () => {
  reset()
  const full = readSnapshot().players[1].health

  // They start 120 units apart and the jab reaches 34, so walk in first —
  // the pushboxes stop them at touching distance.
  for (let i = 0; i < 60; i++) advance(RIGHT, LEFT)

  let sawHitstop = false
  for (let i = 0; i < 40; i++) {
    advance(LP, 0)
    if (readSnapshot().hitstop > 0) sawHitstop = true
  }

  const s = readSnapshot()
  expect(s.players[1].health).toBeLessThan(full)
  expect(sawHitstop).toBe(true)
})

/**
 * The event flags across the real boundary, packed and unpacked. It is the one
 * call the view makes about a frame that is no longer the current one, so a
 * mistake in the packing shows up as effects on the wrong fighter rather than
 * as anything that fails a checksum.
 */
test('a hit reports an event on the frame it landed', () => {
  reset()
  for (let i = 0; i < 60; i++) advance(RIGHT, LEFT)

  let hitFrame = -1
  for (let i = 0; i < 40 && hitFrame < 0; i++) {
    advance(LP, 0)
    const s = readSnapshot()
    // The frame that produced this snapshot is the one before its counter.
    if (eventsAt(s.frame - 1)[1] & EVENT_HIT) hitFrame = s.frame - 1
  }

  expect(hitFrame).toBeGreaterThan(0)
  // On the defender and nobody else, and gone the frame after.
  expect(eventsAt(hitFrame)[0]).toBe(0)
  expect(eventsAt(hitFrame + 1)[1] & EVENT_HIT).toBe(0)
})

test('the frame counter tracks advance calls', () => {
  reset()
  for (let i = 0; i < 42; i++) advance(0, 0)
  expect(readSnapshot().frame).toBe(42)
})

test('walking moves the players at the sim walk speed', () => {
  reset()
  advance(RIGHT, LEFT)
  const s = readSnapshot()
  expect(s.players[0].x).toBeCloseTo(-58.5, 5) // -60 + 1.5
  expect(s.players[1].x).toBeCloseTo(58.5, 5) //   60 - 1.5
})

test('jumping leaves the ground and comes back', () => {
  reset()
  // Pre-jump is grounded — that is what makes throws beat jump attempts.
  for (let i = 0; i < PRE_JUMP; i++) {
    advance(UP, 0)
    expect(readSnapshot().players[0].y).toBe(0)
  }

  advance(0, 0)
  expect(readSnapshot().players[0].y).toBeGreaterThan(0)

  for (let i = 0; i < 80; i++) advance(0, 0)
  const s = readSnapshot()
  expect(s.players[0].y).toBe(0)
  expect(s.players[1].y).toBe(0)
})

test('walls clamp both players', () => {
  reset()
  for (let i = 0; i < 400; i++) advance(LEFT, RIGHT)
  const s = readSnapshot()
  expect(s.players[0].x).toBeCloseTo(-188, 5) // -200 + 12
  expect(s.players[1].x).toBeCloseTo(188, 5)
})

test('the same inputs produce the same snapshot bytes', () => {
  const play = () => {
    reset()
    for (let f = 0; f < 600; f++) advance((f * 7) % 13 & 0xf, (f * 11) % 17 & 0xf)
    return JSON.stringify(readSnapshot())
  }
  expect(play()).toBe(play())
})

// A fireball is the first thing in the game that outlives the move that made
// it, and the first state the view draws that is not a player. Both sides of
// the boundary have to agree that it exists.
test('a fireball crosses the boundary and travels', () => {
  reset()
  expect(readSnapshot().projectiles).toEqual([])

  // QCF + LP, then wait out the startup.
  advance(DOWN, 0)
  advance(DOWN | RIGHT, 0)
  advance(RIGHT | LP, 0)
  for (let i = 0; i < 20 && readSnapshot().projectiles.length === 0; i++) advance(0, 0)

  const [ball] = readSnapshot().projectiles
  expect(ball).toBeDefined()
  expect(ball.w).toBeGreaterThan(0)

  advance(0, 0)
  expect(readSnapshot().projectiles[0].x).toBeGreaterThan(ball.x)
})
