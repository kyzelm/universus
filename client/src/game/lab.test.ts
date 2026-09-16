import {expect, test} from 'vitest'
import {advantageText, createLab, line, numpad} from './lab'
import {IN_DOWN, IN_LEFT, IN_LK, IN_LP, IN_MK, IN_MP, IN_RIGHT, IN_UP} from './input'
import type {Snapshot} from '../sim/wasm'

/**
 * A snapshot with nothing in it but the two fields the overlay reads. The real
 * one comes from the sim; what matters here is the frame each seat stops and
 * starts being able to act.
 */
function at(frame: number, p1: boolean, p2: boolean): Snapshot {
  const player = (actionable: boolean) => ({actionable: actionable ? 1 : 0})
  return {frame, players: [player(p1), player(p2)]} as unknown as Snapshot
}

const NONE = [0, 0] as const

/** Runs an exchange: both busy from `from`, seat 0 free at `f0`, seat 1 at `f1`. */
function exchange(f0: number, f1: number, from = 10) {
  const lab = createLab()
  for (let f = 0; f < from; f++) lab.step(at(f, true, true), NONE)
  for (let f = from; f <= Math.max(f0, f1); f++) {
    lab.step(at(f, f >= f0, f >= f1), NONE)
  }
  return lab
}

test('advantage is the gap between the two recoveries', () => {
  expect(exchange(30, 33).advantage()).toBe(3)
  expect(exchange(33, 30).advantage()).toBe(-3)
  expect(exchange(30, 30).advantage()).toBe(0)
})

/**
 * The gate the readout needs. One player attacking while the other stands
 * still is not an exchange, and posting a figure for it would report the
 * attacker's whole recovery as the other player's advantage.
 */
test('nothing is reported until both players were busy at once', () => {
  const lab = createLab()
  for (let f = 0; f < 20; f++) lab.step(at(f, f < 5 || f > 12, true), NONE)
  expect(lab.advantage()).toBe(null)
})

test('the figure survives until the next exchange', () => {
  const lab = exchange(30, 35)
  for (let f = 36; f < 90; f++) lab.step(at(f, true, true), NONE)
  expect(lab.advantage()).toBe(5)
})

test('a held input is one row and a count', () => {
  const lab = createLab()
  for (let f = 0; f < 4; f++) lab.step(at(f, true, true), [IN_RIGHT, 0])
  lab.step(at(4, true, true), [IN_RIGHT | IN_LP, 0])

  expect(lab.history(0)).toEqual(['6          4', '6 LP'])
  expect(lab.history(1)).toEqual(['5          5'])
})

test('the history is capped, oldest dropped first', () => {
  const lab = createLab()
  // A different input every frame, so every one of them is its own row.
  for (let f = 0; f < 40; f++) lab.step(at(f, true, true), [f % 2 ? IN_LP : IN_MP, 0])

  const rows = lab.history(0)
  expect(rows.length).toBe(12)
  expect(rows[rows.length - 1]).toBe('5 LP')
})

test('directions read as numpad notation', () => {
  expect(numpad(0)).toBe('5')
  expect(numpad(IN_DOWN)).toBe('2')
  expect(numpad(IN_DOWN | IN_RIGHT)).toBe('3')
  expect(numpad(IN_UP | IN_LEFT)).toBe('7')
})

/** A pair is the input the display exists for: it shows whether both arrived. */
test('a two-button input shows both buttons', () => {
  expect(line({bits: IN_LP | IN_LK, frames: 1})).toBe('5 LP+LK')
  expect(line({bits: IN_MP | IN_MK, frames: 2})).toBe('5 MP+MK    2')
})

test('the readout names the player who acts first', () => {
  expect(advantageText(null)).toBe('')
  expect(advantageText(0)).toBe('EVEN')
  expect(advantageText(4)).toBe('P1 +4')
  expect(advantageText(-4)).toBe('P2 +4')
})
