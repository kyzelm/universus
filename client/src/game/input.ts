/**
 * Keyboard to input bitfield. Bits mirror sim/state.go — the same uint16 goes
 * to the sim, the network, the replay and the server, so this file and that
 * one change together.
 *
 * Attack bits (4-6, 8-10) exist in the format but are unbound: the M0 sim
 * ignores them. They get keys when there are moves to trigger.
 */
export const IN_UP = 1 << 0
export const IN_DOWN = 1 << 1
export const IN_LEFT = 1 << 2
export const IN_RIGHT = 1 << 3

/** KeyboardEvent.code to bit. */
const P1_KEYS: Record<string, number> = {
  KeyW: IN_UP,
  KeyS: IN_DOWN,
  KeyA: IN_LEFT,
  KeyD: IN_RIGHT,
}

const P2_KEYS: Record<string, number> = {
  ArrowUp: IN_UP,
  ArrowDown: IN_DOWN,
  ArrowLeft: IN_LEFT,
  ArrowRight: IN_RIGHT,
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

  const down = (e: Event) => {
    const {code} = e as KeyboardEvent
    if (code in P1_KEYS || code in P2_KEYS) {
      held.add(code)
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

  return {
    poll: () => [packBits(held, P1_KEYS), packBits(held, P2_KEYS)],
    dispose() {
      target.removeEventListener('keydown', down)
      target.removeEventListener('keyup', up)
      target.removeEventListener('blur', clear)
      held.clear()
    },
  }
}