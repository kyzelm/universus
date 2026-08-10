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
 */
export function createClock(stepMs = STEP_MS, maxCatchup = 5): Clock {
  let acc = 0

  return {
    tick(dtMs) {
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