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

test('frame 0 is readable before the first advance', () => {
  reset()
  const s = readSnapshot()
  expect(s.frame).toBe(0)
  expect(s.players).toEqual([
    {x: -60, y: 0},
    {x: 60, y: 0},
  ])
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
  advance(UP, 0)
  expect(readSnapshot().players[0].y).toBeGreaterThan(0)

  for (let i = 0; i < 60; i++) advance(0, 0)
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
