import {expect, test} from 'vitest'
import {createClock, STEP_MS} from './clock'

test('a frame worth of time is exactly one step', () => {
  const c = createClock()
  expect(c.tick(STEP_MS)).toBe(1)
})

// Exact step counts would be testing floating-point luck. The property that
// matters is that the clock tracks real time without drifting away from it.
function stepsOver(dtMs: number, ticks: number): number {
  const c = createClock()
  let steps = 0
  for (let i = 0; i < ticks; i++) steps += c.tick(dtMs)
  return steps
}

test('leftover time carries into the next frame instead of being lost', () => {
  // 10 ms per tick, 16.67 ms per step: 1,1,2,1,1,2... never 0.6 steps.
  const steps = stepsOver(10, 600) // 6 seconds
  expect(steps).toBeGreaterThanOrEqual(359)
  expect(steps).toBeLessThanOrEqual(360)
})

test('a fast display does not run the sim fast, and does not drift', () => {
  // 144 Hz for 10 seconds: most frames step zero times, and the error stays
  // bounded at one step rather than accumulating.
  const steps = stepsOver(1000 / 144, 1440)
  expect(steps).toBeGreaterThanOrEqual(599)
  expect(steps).toBeLessThanOrEqual(600)
})

test('a long stall is capped instead of compounding', () => {
  const c = createClock(STEP_MS, 5)
  expect(c.tick(1000)).toBe(5) // 60 steps' worth of debt, 5 run
  // The backlog is dropped, so the next normal frame is a normal frame.
  expect(c.tick(STEP_MS)).toBe(1)
})

test('sub-step deltas accumulate rather than stepping every call', () => {
  const c = createClock()
  expect(c.tick(1)).toBe(0)
  expect(c.tick(1)).toBe(0)
  expect(c.tick(STEP_MS - 2)).toBe(1)
})