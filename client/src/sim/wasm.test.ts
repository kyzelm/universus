import {readFileSync} from 'node:fs'
import {beforeAll, expect, test} from 'vitest'
import '../../public/wasm_exec.js'
import {advance, initSim, readSnapshot, reset} from './wasm'

// The Go build and the TypeScript reader agree on a byte layout or they do
// not. This runs the real module off disk and checks that they do.
beforeAll(async () => {
  // Relative to the vitest root (client/); import.meta.url is an http URL here.
  const wasm = readFileSync('public/main.wasm')
  await initSim(wasm)
})

// Input bits, mirroring sim/state.go.
const UP = 1 << 0
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
