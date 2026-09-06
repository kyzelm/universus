import {EVENT_RING} from '../sim/wasm'

/**
 * Firing effects without firing them eight times.
 *
 * **The classic rollback bug is one hit sound playing once per replay.** The
 * sim avoids it by setting flags in state rather than firing anything (see
 * sim/events.go); this is the other half — the view fires effects only for
 * frames that can no longer be simulated again, and never fires a frame twice.
 *
 * Two rules and nothing else:
 *   1. Only frames at or below the confirmed line, because a predicted frame
 *      can still be rolled back and its events replaced.
 *   2. Only frames not already fired, because the confirmed line is read every
 *      display frame and mostly has not moved.
 */
export interface Fired {
  frame: number
  /** 0 or 1 — the player the effect happened *to*, which is where to draw it. */
  seat: number
  /** The EVENT_* bits set on that seat for that frame. */
  bits: number
}

export interface Events {
  /** Fires everything settled since the last call, oldest frame first. */
  drain(confirmed: number, fire: (f: Fired) => void): void
  /** Forgets what has been fired. For a new match, which starts at frame 0. */
  reset(): void
}

/**
 * @param read the sim's per-frame event words, one per seat. Called only for
 * frames inside the sim's own ring; older ones read as zero there anyway,
 * which is why that ring is where the catch-up below stops.
 */
export function createEvents(read: (frame: number) => readonly [number, number]): Events {
  let firedThrough = -1

  return {
    drain(confirmed, fire) {
      // A backgrounded tab can confirm hundreds of frames at once. The sim has
      // long since overwritten them, so this is where the loop stops rather
      // than a decision about how many effects are too many.
      const from = Math.max(firedThrough + 1, confirmed - EVENT_RING + 1)

      for (let f = from; f <= confirmed; f++) {
        const bits = read(f)
        for (let seat = 0; seat < bits.length; seat++) {
          if (bits[seat] !== 0) fire({frame: f, seat, bits: bits[seat]})
        }
      }
      if (confirmed > firedThrough) firedThrough = confirmed
    },

    reset() {
      firedThrough = -1
    },
  }
}
