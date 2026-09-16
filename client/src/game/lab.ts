import type {Snapshot} from '../sim/wasm'
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
 * The training overlay: frame advantage and an input display.
 *
 * Both are instrumentation rather than content (03 Game Design/Game Modes.md).
 * The frame advantage readout is how the frame data in data/*.json gets checked
 * against what the game does — a jab that the file says is +2 on block and the
 * readout says is -1 is a bug in one of the two, and without the readout it is
 * a bug nobody sees. The input display is how a dropped input is told from a
 * wrong one: a special that did not come out either was not recognised or was
 * never pressed, and those have different fixes.
 *
 * It is a view-side reading of the sim, not a system: nothing here crosses back
 * into sim state, and the numbers it reports are the sim's own — actionability
 * comes over in the snapshot (sim/snapshot.go) rather than being worked out
 * from a state list kept on this side, which would confirm the view's idea of
 * the frame data instead of the sim's.
 */

/** Rows of input history kept per seat. Twelve fills the column on screen. */
const HISTORY = 12

const BUTTONS: readonly (readonly [number, string])[] = [
  [IN_LP, 'LP'],
  [IN_MP, 'MP'],
  [IN_HP, 'HP'],
  [IN_LK, 'LK'],
  [IN_MK, 'MK'],
  [IN_HK, 'HK'],
]

export interface Lab {
  /** Call once per *simulated* frame, with the inputs that frame was fed. */
  step(snap: Snapshot, inputs: readonly [number, number]): void
  /**
   * Player 1's advantage in frames after the last exchange — positive when
   * player 1 acts first. Null until an exchange has been played.
   */
  advantage(): number | null
  /** Input history for a seat, oldest first, one drawable line each. */
  history(seat: number): string[]
  reset(): void
}

export function createLab(): Lab {
  // The frame each seat last became actionable, and whether they were on the
  // previous frame. Advantage is the difference between the two, which is the
  // definition — not a number read out of the frame data.
  const free = [0, 0]
  const was = [true, true]

  // An exchange is a stretch with neither player able to act: an attack met by
  // a block, a hit, a trade, or two moves whiffing at each other. Without this
  // gate, anyone standing still would post an advantage figure every time their
  // opponent recovered from something they had nothing to do with.
  let overlapped = false
  let adv: number | null = null

  const rows: {bits: number; frames: number}[][] = [[], []]

  const record = (seat: number, bits: number): void => {
    const row = rows[seat]
    const last = row[row.length - 1]
    // A held input is one row with a count, not sixty rows. The count is the
    // useful half: it is how long a direction was held before the button.
    if (last && last.bits === bits) {
      last.frames++
      return
    }
    row.push({bits, frames: 1})
    if (row.length > HISTORY) row.shift()
  }

  return {
    step(snap, inputs) {
      for (let seat = 0; seat < 2; seat++) {
        const now = snap.players[seat].actionable === 1
        if (now && !was[seat]) free[seat] = snap.frame
        was[seat] = now
        record(seat, inputs[seat])
      }

      if (!was[0] && !was[1]) {
        overlapped = true
      } else if (was[0] && was[1] && overlapped) {
        adv = free[1] - free[0]
        overlapped = false
      }
    },

    advantage: () => adv,
    history: (seat) => rows[seat].map(line),
    reset() {
      free[0] = free[1] = 0
      was[0] = was[1] = true
      overlapped = false
      adv = null
      rows[0].length = rows[1].length = 0
    },
  }
}

/**
 * Numpad notation, the way every frame data resource in the genre writes a
 * direction. SOCD is resolved before this (game/input.ts), so there is no
 * opposing pair left to decide here.
 */
export function numpad(bits: number): string {
  const y = bits & IN_UP ? 1 : bits & IN_DOWN ? -1 : 0
  const x = bits & IN_RIGHT ? 1 : bits & IN_LEFT ? -1 : 0
  return String(5 + y * 3 + x)
}

/** One row of the input display: direction, buttons, frames held. */
export function line({bits, frames}: {bits: number; frames: number}): string {
  const pressed = BUTTONS.filter(([bit]) => bits & bit).map(([, name]) => name)
  const held = `${numpad(bits)} ${pressed.join('+')}`.trimEnd()
  return frames > 1 ? `${held.padEnd(11)}${frames}` : held
}

/** What the readout says. Positive is player 1 acting first. */
export function advantageText(adv: number | null): string {
  if (adv === null) return ''
  if (adv === 0) return 'EVEN'
  return adv > 0 ? `P1 +${adv}` : `P2 +${-adv}`
}
