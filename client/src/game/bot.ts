import {
  IN_DOWN,
  IN_HK,
  IN_HP,
  IN_LEFT,
  IN_LK,
  IN_LP,
  IN_MK,
  IN_MP,
  IN_RIGHT,
  IN_UP,
} from './input'

/**
 * A seeded input source, for measurement runs.
 *
 * It is a **device, not an AI**: it produces a bitfield exactly where a
 * keyboard or a pad produces one, before anything the sim reads. Nothing here
 * is sim state, nothing here rolls back, and the frames it presses are
 * confirmed and logged like any others — so a measured run is still a
 * replayable one.
 *
 * Why it has to exist before the 60 fps number can be captured: rollback only
 * happens when a prediction is wrong, and prediction repeats the last input.
 * A stick held still never mispredicts however bad the connection is, so a
 * measurement taken with nobody pressing anything reports a frame cost the
 * game never actually pays ([[Measurement Methodology]], M-A).
 */
export interface Bot {
  /** One frame's bitfield. Called once per fixed step, like any other device. */
  poll(): number
  /** Both ends open the same URL, so the seat is what makes the streams differ. */
  reseed(seed: number): void
}

/**
 * Single directions and the two down-diagonals — enough to walk, crouch, jump
 * and occasionally complete a motion, which is what makes the stream worth
 * predicting wrongly. Neutral is in the table because standing still is an
 * input a player makes.
 */
const DIRECTIONS = [0, IN_LEFT, IN_RIGHT, IN_DOWN, IN_UP, IN_DOWN | IN_RIGHT, IN_DOWN | IN_LEFT]
const ATTACKS = [IN_LP, IN_MP, IN_HP, IN_LK, IN_MK, IN_HK]

export function createBot(seed = 1): Bot {
  let s = seed >>> 0 || 1

  // xorshift32, the same generator the sim uses. Not for the same reason —
  // this one never has to agree with the other end — but a second algorithm
  // here would be a second thing to explain.
  const rand = () => {
    s ^= s << 13
    s ^= s >>> 17
    s ^= s << 5
    s >>>= 0
    return s
  }

  let dir = 0
  let dirFrames = 0
  let atk = 0
  let atkFrames = 0

  return {
    reseed(next) {
      s = next >>> 0 || 1
      dirFrames = atkFrames = 0
    },

    poll() {
      if (--dirFrames <= 0) {
        dir = DIRECTIONS[rand() % DIRECTIONS.length]
        dirFrames = 6 + (rand() % 15)
      }

      // A two-frame press and then a gap. A button held down is one input
      // change however long it is held, and one change is one misprediction:
      // the press and the release are the events worth generating.
      if (--atkFrames <= 0) {
        atk = atk ? 0 : ATTACKS[rand() % ATTACKS.length]
        atkFrames = atk ? 2 : 10 + (rand() % 30)
      }

      return dir | atk
    },
  }
}
