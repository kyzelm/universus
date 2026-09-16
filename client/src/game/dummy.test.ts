import {readFileSync} from 'node:fs'
import {beforeAll, expect, test} from 'vitest'
import '../../public/wasm_exec.js'
import {advance, initSim, readSnapshot, reset, STATE_NAMES} from '../sim/wasm'
import {createDummy, type DummyMode} from './dummy'
import {IN_DOWN, IN_HK, IN_LEFT, IN_LP, IN_RIGHT, IN_UP} from './input'
import type {Snapshot} from '../sim/wasm'

beforeAll(async () => {
  await initSim(readFileSync('public/main.wasm'))
})

/** Only the fields the dummy reads. The real ones come from the sim below. */
function seen(opts: {facing?: number; actionable?: boolean; attacking?: boolean} = {}): Snapshot {
  const {facing = -1, actionable = true, attacking = false} = opts
  return {
    players: [{moveIndex: attacking ? 0 : -1}, {facing, actionable: actionable ? 1 : 0}],
  } as unknown as Snapshot
}

function dummyIn(mode: DummyMode, snap = seen()): number {
  const d = createDummy()
  d.setMode(mode)
  return d.poll(snap, 0)
}

test('the plain behaviours are one held input', () => {
  expect(dummyIn('stand')).toBe(0)
  expect(dummyIn('crouch')).toBe(IN_DOWN)
  expect(dummyIn('jump')).toBe(IN_UP)
})

/**
 * Held away *only* while the attacker is in a move. A dummy holding back all
 * match is one walking into the corner, and the setting exists to give a
 * stationary target that blocks.
 */
test('block holds away at an attack and nothing otherwise', () => {
  expect(dummyIn('block', seen({facing: -1, attacking: true}))).toBe(IN_RIGHT)
  expect(dummyIn('block', seen({facing: 1, attacking: true}))).toBe(IN_LEFT)
  expect(dummyIn('block', seen({attacking: false}))).toBe(0)
})

test('reversal blocks while it can act and works the motion while it cannot', () => {
  const d = createDummy()
  d.setMode('reversal')
  expect(d.poll(seen({actionable: true, attacking: true}), 0)).toBe(IN_RIGHT)

  // Facing left, so forward is LEFT. The motion is the shortcut → ↓ →, with
  // no diagonal: looping the full → ↓ ↘ spells a double quarter-circle across
  // two turns and reverses with a super.
  const stunned = seen({facing: -1, actionable: false})
  expect(d.poll(stunned, 0)).toBe(IN_LEFT)
  expect(d.poll(stunned, 0)).toBe(IN_DOWN)
  expect(d.poll(stunned, 0)).toBe(IN_LEFT | IN_LP)
})

/**
 * **The assumption the whole reversal setting rests on**, checked against the
 * real sim rather than argued: a dummy that knows nothing about how long a
 * knockdown lasts still reverses on wakeup, because the input buffer holds the
 * press and spends it on the first actionable frame. If the buffer, the motion
 * table or the wakeup rules move, this is what fails.
 *
 * The dummy leaving the ground is what makes it a *dragon punch* and not a
 * buffered jab: nothing else it presses in this mode rises, and a reversal that
 * came out as a jab would be the motion silently not registering.
 */
test('the dummy reverses out of a knockdown', () => {
  reset(true)
  const d = createDummy()
  d.setMode('stand')

  const step = (p1: number) => {
    const p2 = d.poll(readSnapshot(), 0)
    advance(p1, p2)
    return readSnapshot()
  }

  // Walk into range, then sweep — 2HK is the roster's knockdown strike, so the
  // dummy ends up on the floor with a wakeup to reverse on. It is set to stand
  // for this half: a reversal dummy blocks, and a blocked sweep knocks nobody
  // down.
  for (let f = 0; f < 60; f++) step(IN_RIGHT)
  let knocked = false
  for (let f = 0; f < 60 && !knocked; f++) {
    const snap = step(f < 2 ? IN_DOWN | IN_HK : 0)
    knocked = STATE_NAMES[snap.players[1].state] === 'knockdown'
  }
  expect(knocked).toBe(true)

  // From here player 1 stands still, so nothing but the dummy can produce an
  // attack — and nothing it presses but the dragon punch can leave the ground.
  d.setMode('reversal')
  let rose = false
  for (let f = 0; f < 180 && !rose; f++) rose = step(0).players[1].y > 0
  expect(rose).toBe(true)
})

/**
 * Recording is the seat playing itself with the presses kept: it passes the
 * keyboard through, so what you watch while recording is what plays back.
 */
test('a take is recorded, passed through, and looped', () => {
  const d = createDummy()
  const snap = seen()

  d.setMode('record')
  const take = [IN_DOWN, IN_DOWN | IN_LP, 0]
  expect(take.map((bits) => d.poll(snap, bits))).toEqual(take)
  expect(d.frames()).toBe(3)

  d.setMode('playback')
  // Twice through, to show the loop rather than the first pass. The keyboard
  // is pressing something else meanwhile and is ignored.
  const played = [...take, ...take].map(() => d.poll(snap, IN_UP))
  expect(played).toEqual([...take, ...take])
})

test('playback with nothing recorded stands still', () => {
  const d = createDummy()
  d.setMode('playback')
  expect(d.poll(seen(), IN_UP)).toBe(0)
})

/** Entering record is what starts a take, so there is no stale half of one. */
test('recording again replaces the previous take', () => {
  const d = createDummy()
  const snap = seen()

  d.setMode('record')
  d.poll(snap, IN_DOWN)
  d.poll(snap, IN_DOWN)

  d.setMode('record')
  d.poll(snap, IN_LP)
  expect(d.frames()).toBe(1)

  d.setMode('playback')
  expect(d.poll(snap, 0)).toBe(IN_LP)
  expect(d.poll(snap, 0)).toBe(IN_LP)
})

/** The cap stops appending rather than wrapping: a take that dropped its own
 * beginning would play back a setup nobody performed. */
test('a take stops at the cap', () => {
  const d = createDummy()
  const snap = seen()

  d.setMode('record')
  for (let f = 0; f < 700; f++) d.poll(snap, f < 600 ? IN_DOWN : IN_UP)

  expect(d.frames()).toBe(600)
  d.setMode('playback')
  expect(d.poll(snap, 0)).toBe(IN_DOWN)
})
