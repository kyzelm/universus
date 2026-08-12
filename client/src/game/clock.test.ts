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
// A 60 Hz display driving a 60 Hz sim must be one step per frame, every frame.
// Without snapping, sub-millisecond jitter makes ~3% of frames produce no step
// at all — and a frame with no step never polls input, so that press lands
// late. Average rate is fine either way; the latency is what suffers.
test('a jittery 60Hz display produces exactly one step per frame', () => {
  const clock = createClock()
  const counts = new Map<number, number>()

  // Deterministic jitter of +-1ms around a real 60Hz delta.
  for (let i = 0; i < 1000; i++) {
    const jitter = Math.sin(i * 12.9898) // in [-1, 1]
    const n = clock.tick(STEP_MS + jitter)
    counts.set(n, (counts.get(n) ?? 0) + 1)
  }

  expect(counts.get(1)).toBe(1000)
})

// Snapping must not swallow rates that genuinely are not 60.
test('other refresh rates still go through the accumulator', () => {
  const at = (hz: number, frames: number) => {
    const clock = createClock()
    let steps = 0
    for (let i = 0; i < frames; i++) steps += clock.tick(1000 / hz)
    return steps
  }

  // 144Hz: many display frames per sim step, so about 60 steps a second.
  expect(at(144, 144)).toBeGreaterThanOrEqual(59)
  expect(at(144, 144)).toBeLessThanOrEqual(61)

  // 50Hz: more than one step per frame on average, still ~60 a second.
  expect(at(50, 50)).toBeGreaterThanOrEqual(59)
  expect(at(50, 50)).toBeLessThanOrEqual(61)

  // 30Hz: two steps per frame.
  expect(at(30, 30)).toBeGreaterThanOrEqual(59)
  expect(at(30, 30)).toBeLessThanOrEqual(61)
})

// A long stall must not turn into a burst that locks the tab.
test('a stalled frame is capped, not compounded', () => {
  const clock = createClock()
  expect(clock.tick(5000)).toBe(5)
  expect(clock.tick(STEP_MS)).toBe(1)
})
