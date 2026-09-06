import {expect, test} from 'vitest'
import {createEvents, type Fired} from './events'
import {EVENT_BLOCK, EVENT_HIT, EVENT_RING} from '../sim/wasm'

/** A stand-in for the sim's ring: whatever the test puts in it. */
function ring(entries: Record<number, [number, number]>) {
  const fired: Fired[] = []
  const events = createEvents((f) => entries[f] ?? [0, 0])
  return {
    fired,
    events,
    drain: (confirmed: number) => events.drain(confirmed, (f) => fired.push(f)),
  }
}

test('fires once per event, oldest frame first', () => {
  const {fired, drain} = ring({1: [0, EVENT_HIT], 3: [EVENT_BLOCK, 0]})
  drain(4)

  expect(fired).toEqual([
    {frame: 1, seat: 1, bits: EVENT_HIT},
    {frame: 3, seat: 0, bits: EVENT_BLOCK},
  ])
})

test('a frame with nothing on it fires nothing', () => {
  const {fired, drain} = ring({})
  drain(30)
  expect(fired).toEqual([])
})

/**
 * **The bug this file exists for.** The confirmed line is read every display
 * frame and mostly has not moved; a view that fired on what it sees would fire
 * the same hit on every one of them.
 */
test('draining again fires nothing new', () => {
  const {fired, drain} = ring({2: [0, EVENT_HIT]})
  drain(5)
  expect(fired.length).toBe(1)

  for (let i = 0; i < 10; i++) drain(5)
  expect(fired.length).toBe(1)
})

/**
 * The other half: a predicted frame can still be rolled back and its events
 * replaced, so nothing fires until the frame is settled — and what fires then
 * is the corrected value, not the guess.
 */
test('a predicted frame fires only once it is settled, and only its final value', () => {
  const entries: Record<number, [number, number]> = {4: [0, EVENT_HIT]}
  const fired: Fired[] = []
  const events = createEvents((f) => entries[f] ?? [0, 0])

  // Frame 4 has been simulated on a guess, but only frame 2 is settled.
  events.drain(2, (f) => fired.push(f))
  expect(fired).toEqual([])

  // The packet arrives: the defender was blocking, and the replay says so.
  entries[4] = [0, EVENT_BLOCK]
  events.drain(6, (f) => fired.push(f))

  expect(fired).toEqual([{frame: 4, seat: 1, bits: EVENT_BLOCK}])
})

test('nothing fires before anything is confirmed', () => {
  const {fired, drain} = ring({0: [EVENT_HIT, 0]})
  drain(-1)
  expect(fired).toEqual([])
})

/**
 * A backgrounded tab confirms hundreds of frames at once, and the sim kept
 * none of them. The catch-up stops where the sim's ring does rather than
 * replaying a minute of hit sparks at a player who has just come back.
 */
test('catching up stops at the end of the sim ring', () => {
  const reads: number[] = []
  const events = createEvents((f) => {
    reads.push(f)
    return [0, 0]
  })
  events.drain(1000, () => {})

  expect(reads.length).toBe(EVENT_RING)
  expect(reads[0]).toBe(1000 - EVENT_RING + 1)
})
