/**
 * Fixed-timestep accumulator. The sim runs at exactly 60 Hz regardless of what
 * the display does — a 144 Hz monitor must not run the game 2.4× fast, and a
 * slow frame must not skip physics.
 */
export interface Clock {
  /** Steps to run for an elapsed wall-clock delta. */
  tick(dtMs: number): number
}

export const STEP_MS = 1000 / 60

/**
 * @param maxCatchup steps per frame before the leftover time is dropped. A
 * frame that takes longer than the steps it schedules would otherwise ask for
 * more steps next frame, and so on, until the tab locks up.
 * @param snapMs how far a frame delta may sit from a whole number of steps and
 * still be treated as that many. See below.
 */
export function createClock(stepMs = STEP_MS, maxCatchup = 5, snapMs = 1.5): Clock {
  let acc = 0

  return {
    tick(dtMs) {
      // Frame-rate snapping. A 60 Hz display feeding a 60 Hz sim is the worst
      // case for a bare accumulator: the delta lands a hair either side of one
      // step, so the remainder drifts and roughly 3% of display frames produce
      // *zero* steps while another 3% produce two. A zero-step frame does not
      // poll input, so that press lands a frame late — inconsistent latency
      // and judder, from a clock that is on average perfectly correct.
      //
      // Snapping a near-miss to the whole step it obviously meant costs at most
      // snapMs of real time per frame and makes one display frame reliably one
      // sim frame. Deltas that are not near a whole step — 50 Hz, 144 Hz, a
      // genuinely long frame — miss the test and fall through to the
      // accumulator, which is what handles them correctly.
      const whole = Math.round(dtMs / stepMs)
      if (whole >= 1 && Math.abs(dtMs - whole * stepMs) <= snapMs) {
        dtMs = whole * stepMs
      }

      acc += dtMs

      let steps = 0
      while (acc >= stepMs && steps < maxCatchup) {
        acc -= stepMs
        steps++
      }

      // Hit the ceiling: the machine is behind, so give up on the backlog
      // rather than compounding it. Real time is lost; determinism is not.
      if (steps === maxCatchup) acc = 0

      return steps
    },
  }
}