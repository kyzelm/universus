/**
 * Devices to input bitfield. Bits mirror sim/state.go — the same uint16 goes
 * to the sim, the network, the replay and the server, so this file and that
 * one change together.
 *
 * Bits 7 and 11-15 are reserved and must stay clear.
 */
export const IN_UP = 1 << 0
export const IN_DOWN = 1 << 1
export const IN_LEFT = 1 << 2
export const IN_RIGHT = 1 << 3
export const IN_LP = 1 << 4
export const IN_MP = 1 << 5
export const IN_HP = 1 << 6
export const IN_LK = 1 << 8
export const IN_MK = 1 << 9
export const IN_HK = 1 << 10

/**
 * Two buttons on one key, which is how throws are played everywhere.
 *
 * A throw is LP+LK *on the same frame*, and the sim's press edge is generous
 * about which frame that is — the pair completing is the press. What it cannot
 * help with is the frame before: LP alone is a jab, the jab starts, and the LK
 * arriving a frame later finds a player who is no longer actionable. Pressing
 * both keys in the same 16.6 ms is the hard part, and one key does it exactly.
 *
 * This belongs here and nowhere else: a macro is a per-user binding, and the
 * invariant is that every local setting resolves before the bitfield is built
 * (CLAUDE.md). The sim sees LP+LK and cannot tell which key produced them, so
 * a player who prefers the two keys is playing the same game.
 */
const THROW = IN_LP | IN_LK

/** KeyboardEvent.code to bit — or to a mask, for the macro. */
const P1_KEYS: Record<string, number> = {
  KeyW: IN_UP,
  KeyS: IN_DOWN,
  KeyA: IN_LEFT,
  KeyD: IN_RIGHT,
  KeyU: IN_LP,
  KeyI: IN_MP,
  KeyO: IN_HP,
  KeyJ: IN_LK,
  KeyK: IN_MK,
  KeyL: IN_HK,
  KeyH: THROW,
}

const P2_KEYS: Record<string, number> = {
  ArrowUp: IN_UP,
  ArrowDown: IN_DOWN,
  ArrowLeft: IN_LEFT,
  ArrowRight: IN_RIGHT,
  Numpad7: IN_LP,
  Numpad8: IN_MP,
  Numpad9: IN_HP,
  Numpad4: IN_LK,
  Numpad5: IN_MK,
  Numpad6: IN_HK,
  Numpad0: THROW,
}

/**
 * Standard Gamepad mapping, button index to bit. The six-attack layout every
 * fighting game uses: punches on the top row, kicks on the bottom.
 *
 * A leverless controller presents as an ordinary gamepad and needs nothing
 * special here — correct SOCD is the whole of its handling, and that already
 * happens below.
 */
const PAD_BUTTONS: Record<number, number> = {
  12: IN_UP,
  13: IN_DOWN,
  14: IN_LEFT,
  15: IN_RIGHT,
  2: IN_LP, // square / X
  3: IN_MP, // triangle / Y
  5: IN_HP, // R1 / RB
  0: IN_LK, // cross / A
  1: IN_MK, // circle / B
  7: IN_HK, // R2 / RT
  4: THROW, // L1 / LB — where every fighting game puts throw
}

/**
 * Stick deflection past which an axis counts as held. Generous, because this
 * is a digital reading of an analog stick, and half-committed diagonals are
 * the enemy of clean motions.
 */
const DEADZONE = 0.5

/**
 * Reads one gamepad into a bitfield. Floats live here and stop here: the sim
 * never sees an axis, only the bit this produced.
 */
export function packPad(pad: Gamepad): number {
  let bits = 0
  for (const index in PAD_BUTTONS) {
    if (pad.buttons[index]?.pressed) bits |= PAD_BUTTONS[index]
  }

  const [x, y] = pad.axes
  if (x <= -DEADZONE) bits |= IN_LEFT
  if (x >= DEADZONE) bits |= IN_RIGHT
  if (y <= -DEADZONE) bits |= IN_UP // browser Y is negative up
  if (y >= DEADZONE) bits |= IN_DOWN

  return bits
}

/**
 * Simultaneous Opposite Cardinal Directions. Left+right resolves to neutral,
 * up beats down.
 *
 * This runs client-side, before packing, on purpose: the sim must never see a
 * value that depends on a local setting. When negative edge and rebinding
 * arrive they resolve here too.
 */
export function resolveSOCD(bits: number): number {
  if (bits & IN_LEFT && bits & IN_RIGHT) bits &= ~(IN_LEFT | IN_RIGHT)
  if (bits & IN_UP && bits & IN_DOWN) bits &= ~IN_DOWN
  return bits
}

export function packBits(held: ReadonlySet<string>, keys: Record<string, number>): number {
  let bits = 0
  for (const code in keys) {
    if (held.has(code)) bits |= keys[code]
  }
  return resolveSOCD(bits)
}

export interface Input {
  /** Both players' bitfields for the current frame. */
  poll(): [number, number]

  dispose(): void
}

/**
 * Tracks held keys and packs them on demand.
 *
 * The loop *polls* this once per fixed step — it is never driven by the key
 * events themselves. A tap and a release inside one frame is a real input the
 * player made, and an event-driven path would either drop it or apply it to
 * the wrong frame. Both are desyncs that surface days later.
 */
export function createInput(target: EventTarget = window): Input {
  const held = new Set<string>()

  // Keys pressed since the last poll, whether or not they are still down. A
  // tap that starts and ends between two polls is a real input the player
  // made; without this latch it lands on no frame at all and vanishes. It is
  // held for exactly one frame, which is the finest the sim can represent.
  const tapped = new Set<string>()

  const down = (e: Event) => {
    const {code} = e as KeyboardEvent
    if (code in P1_KEYS || code in P2_KEYS) {
      held.add(code)
      tapped.add(code)
      e.preventDefault() // arrows scroll the page otherwise
    }
  }
  const up = (e: Event) => held.delete((e as KeyboardEvent).code)

  // Losing focus mid-hold leaves the key stuck down forever; the keyup lands
  // on whatever the user tabbed to.
  const clear = () => held.clear()

  target.addEventListener('keydown', down)
  target.addEventListener('keyup', up)
  target.addEventListener('blur', clear)

  // The Gamepad API is a polling API by design, which is exactly the shape
  // this loop wants: gamepads are read in poll(), never from an event.
  const pads = (): (Gamepad | null)[] => navigator.getGamepads?.() ?? []

  return {
    poll() {
      const pressed = new Set([...held, ...tapped])
      tapped.clear()

      // First two connected pads take seat 0 and seat 1. getGamepads returns a
      // sparse array with holes for disconnected slots, so filter before
      // indexing or unplugging pad 1 silently promotes pad 2.
      const connected = pads().filter((p): p is Gamepad => p !== null)

      // Resolve SOCD once more after merging: each device is clean on its own,
      // but keyboard-left plus pad-right is an opposing pair that only exists
      // after the merge, and an unresolved pair must never reach the sim.
      return [
        resolveSOCD(packBits(pressed, P1_KEYS) | (connected[0] ? packPad(connected[0]) : 0)),
        resolveSOCD(packBits(pressed, P2_KEYS) | (connected[1] ? packPad(connected[1]) : 0)),
      ]
    },
    dispose() {
      target.removeEventListener('keydown', down)
      target.removeEventListener('keyup', up)
      target.removeEventListener('blur', clear)
      held.clear()
      tapped.clear()
    },
  }
}