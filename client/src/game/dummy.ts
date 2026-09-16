import type {Snapshot} from '../sim/wasm'
import {IN_DOWN, IN_LEFT, IN_LP, IN_RIGHT, IN_UP} from './input'

/**
 * The training dummy: the second half of the lab overlay (03 Game Design/Game
 * Modes.md), and what makes the frame advantage readout readable by one person.
 * On-block advantage needs somebody holding back while you attack, and a solo
 * player has two hands.
 *
 * **It is a device, not sim state and not an AI.** It produces a bitfield for
 * seat 2 exactly where that seat's keyboard produces one, and the sim cannot
 * tell the difference — the same contract game/bot.ts already keeps. The
 * tempting alternative is a behaviour field in `GameState` with the sim
 * synthesising the inputs, and it breaks the replay log: the log records what
 * the sim was fed from outside, so every lab recording would play back a dummy
 * that stood still while the match it came from had one that blocked. A device
 * is recorded like any other press and tools/replay reproduces the session.
 *
 * It reads the snapshot to decide, which is reading, not writing: the same
 * thing a player does with their eyes, one frame later than the sim knows it.
 */

export const DUMMY_MODES = [
  'manual',
  'stand',
  'crouch',
  'block',
  'jump',
  'reversal',
  'record',
  'playback',
] as const

/**
 * Ten seconds of recording. A cap rather than a ring: a recording that quietly
 * dropped its own beginning would play back a sequence nobody performed, and
 * the setup being practised is always the start of one.
 */
const MAX_RECORD = 600

export type DummyMode = (typeof DUMMY_MODES)[number]

export interface Dummy {
  mode(): DummyMode
  /** Number keys pick the behaviour: 0 hands the seat back to the keyboard. */
  setMode(mode: DummyMode): void
  /** Frames in the recording, for the label on screen. */
  frames(): number
  /**
   * One frame's bitfield, from the state as of the frame just simulated.
   * `manual` is what seat 2's own keyboard is pressing this frame — what the
   * dummy passes through, and what it records.
   */
  poll(snap: Snapshot, manual: number): number
}

export function createDummy(): Dummy {
  let mode: DummyMode = 'manual'

  // Where the reversal is in its motion. View-side, like every other thing a
  // device remembers between frames — it decides what to press, never what the
  // press means.
  let beat = 0

  // The recording, and where playback is inside it.
  let tape: number[] = []
  let head = 0

  return {
    mode: () => mode,
    setMode(next) {
      // Entering record starts a new take; entering playback rewinds to the
      // top of the one that exists. Leaving record is what ends a recording,
      // so there is no second key to forget to press.
      if (next === 'record') tape = []
      if (next === 'playback') head = 0
      mode = next
      beat = 0
    },

    frames: () => tape.length,

    poll(snap, manual) {
      const me = snap.players[1]
      const them = snap.players[0]
      // Facing is +1 for a player looking right, so away is the other way.
      // Absolute bits, because the sim turns them into facing-relative
      // directions itself and a device that pre-resolved them would be
      // deciding a gameplay question.
      const fwd = me.facing > 0 ? IN_RIGHT : IN_LEFT
      const back = me.facing > 0 ? IN_LEFT : IN_RIGHT

      // Holding away only while the attacker is committed to something. Held
      // permanently it would be a dummy walking into the corner, and the point
      // of the setting is a stationary target that blocks what arrives.
      //
      // ponytail: blocks anything the attacker starts, high or low, because
      // guarding the wrong height is a mechanic the sim does not have yet.
      const guard = () => (them.moveIndex >= 0 ? back : 0)

      switch (mode) {
        case 'manual':
          return manual
        case 'record':
          // The seat plays itself and the presses are kept. Recording past the
          // cap stops appending rather than wrapping, so what plays back is
          // always the beginning of what was performed.
          if (tape.length < MAX_RECORD) tape.push(manual)
          return manual
        case 'playback':
          // Looped, with no gap: a setup worth practising is one that repeats,
          // and a dummy that played its take once would need a key to ask for
          // it again.
          //
          // ponytail: the bits are absolute, so a take recorded on one side of
          // the opponent plays back mirrored on the other. Record it where it
          // is meant to happen; the alternative is storing facing-relative
          // input, which is a gameplay decision and does not belong in a device.
          return tape.length > 0 ? tape[head++ % tape.length] : 0
        case 'stand':
          return 0
        case 'crouch':
          return IN_DOWN
        case 'block':
          return guard()
        case 'jump':
          // Pressed while it can act, released while it cannot — which in the
          // air is the whole jump. One press is one jump (D100), so a dummy
          // that simply held up would jump once and then stand there; letting
          // go on the way up is what arms the next one.
          return me.actionable === 1 ? IN_UP : 0
        case 'reversal': {
          if (me.actionable === 1) {
            beat = 0
            return guard()
          }
          // Not actionable: knocked down, in hitstun, in blockstun. Run the
          // dragon punch on a loop rather than trying to time the wakeup —
          // the sim's input buffer holds a press for ~6 frames and spends it
          // on the first actionable frame, which is exactly how a player makes
          // a reversal, and it means the dummy needs to know nothing about how
          // long the stun is.
          //
          // **It is the shortcut motion, → ↓ →, and that is not a shortcut
          // here.** Looping the real → ↓ ↘ spells a double quarter-circle
          // across two turns of the loop — ↓ ↘ → ↓ ↘ → is right there in
          // → ↓ ↘ → ↓ ↘ → — and the supers are scanned before DP, so the
          // dummy reversed with a level 1 every time, paid for by the lab's
          // infinite meter. The shortcut row has no diagonal in it at all, so
          // no quarter-circle can be read out of the loop however long it runs.
          const step = beat++ % 3
          if (step === 0) return fwd
          if (step === 1) return IN_DOWN
          // The button lands on the frame that completes the motion: the
          // motion is judged at the frame of the press, not at the frame the
          // move comes out.
          return fwd | IN_LP
        }
      }
    },
  }
}
