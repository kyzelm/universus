import {readFileSync} from 'node:fs'
import {afterEach, expect, test, vi} from 'vitest'
import '../../public/wasm_exec.js'
import {createLink} from './impair'
import {CHECKSUM_EVERY, createNetplay, MAX_ROLLBACK, type SimBridge} from './netplay'

/**
 * **The bot-vs-bot harness** (D49, 05 Tooling/Testing and CI.md).
 *
 * Two headless clients, two *real* simulations, scripted inputs and artificial
 * latency. With one machine and no partner on call, this is the primary
 * desync-detection mechanism in the project: it is the only test that can catch
 * two sims disagreeing about state, because it is the only one where two sims
 * exist.
 *
 * The netplay tests beside this one drive a stub sim, deliberately — they are
 * about the driver's bookkeeping, and a stub makes a wrong prediction visible.
 * A stub cannot desync, so it cannot detect one. Here both ends run the shipped
 * WASM binary, exchange checksums over the wire like a real match, and the
 * assertion is the one that matters: the two states agree.
 *
 * The opponents are the scripted AI played as *devices* (sim/ai.go). Random
 * input is dense but shallow; the AI produces the sequences desyncs actually
 * live in — combos, blocks, wakeups, corners — and it is the same rule list
 * that ships. A seat the sim plays would be no use here: both ends would
 * generate the opponent's inputs locally, every prediction would be right, and
 * the netcode would never be exercised at all.
 */

/** 100 ms round trip, 15 ms of jitter, 5% loss — a cell of the M-B matrix. */
const CONDITIONS = {delayMs: 50, jitterMs: 15, lossPercent: 5}

const STEP_MS = 1000 / 60

/**
 * **Long enough to cross a round boundary**, which is 99 seconds of play plus
 * its transitions. That is not a round number picked for coverage: the first
 * bug found in the scripted opponent after it shipped was the round reset
 * dropping its difficulty tier, and every test that existed played inside round
 * one. A transition is also where input stops and restarts on both ends at
 * once, which is the netcode's turn to be wrong.
 *
 * About a second and a half of real time per match.
 */
const FRAMES = Number(process.env.BOT_FRAMES) || 7200

/**
 * How many seed pairs to play. One in CI; raise it to soak, which is the point
 * of having the harness at all (R10) — `BOT_SEEDS=50 pnpm exec vitest run
 * botmatch` is an unattended desync hunt, and every pair is reproducible from
 * its seed.
 */
const MATCHES = Number(process.env.BOT_SEEDS) || 1

declare class Go {
  importObject: WebAssembly.Imports
  run(instance: WebAssembly.Instance): Promise<void>
}

interface End extends SimBridge {
  /** The scripted opponent, pressing for this end's own seat. */
  press(seat: 0 | 1): number
  frame(): number
  /** Every state field as text — used here to read the health bars. */
  dump(): string
}

/**
 * One headless client: its own instance of the WASM module, and therefore its
 * own sim, its own memory and its own devices.
 *
 * Two instances in one process is the whole trick. The Go runtime publishes its
 * API on `globalThis.sim`, so the second instantiation overwrites the first —
 * capturing each one immediately after its `run` is what keeps them apart, and
 * the closures behind each API hold their own module's session.
 */
async function client(seed: number, corruptAt = -1): Promise<End> {
  const go = new Go()
  const {instance} = await WebAssembly.instantiate(
    readFileSync('public/main.wasm'),
    go.importObject,
  )
  // Resolves only when the sim quits, so deliberately not awaited; the API is
  // published by the time run returns.
  void go.run(instance)

  const api = (globalThis as {sim?: Record<string, (...a: number[]) => number>}).sim
  if (!api) throw new Error('the WASM module published no API')

  return {
    // corruptAt flips one bit of one frame's remote input on this end only,
    // which is what a desync looks like from the inside: one bad frame, and
    // every state after it diverges. It is how the detector below is shown to
    // fire.
    advance: (p1, p2) => void api.advance(p1, api.frame() === corruptAt ? p2 ^ 1 : p2),
    rewind: (f) => Boolean(api.rewind(f)),
    checksum: () => api.checksum(),
    dataVersion: () => api.dataVersion(),
    press: (seat) => api.aiPress(seat, 3, seed),
    frame: () => api.frame(),
    dump: () => api.dump() as unknown as string,
  }
}

/**
 * Damage dealt across the whole match, per seat, off the state dump.
 *
 * Not the health bars: health resets between rounds, so a run that happens to
 * end just after a round transition reads as a match where nobody was ever hit.
 * `Dealt` is cumulative for exactly that reason — it exists for the round-cap
 * tiebreak — and it is what says the bots fought rather than circled.
 */
function round(end: End): number {
  return Number(/^Round = (\d+)$/m.exec(end.dump())?.[1] ?? 0)
}

function dealt(end: End): number[] {
  return [...end.dump().matchAll(/Dealt\[\d] = (-?\d+)/g)].map((m) => Number(m[1]))
}

afterEach(() => vi.useRealTimers())

/**
 * Plays the two ends against each other and hands back everything worth
 * asserting on.
 *
 * @param corruptAt a frame on which end B simulates the wrong remote input, or
 * -1 for an honest match.
 */
async function botMatch(corruptAt = -1, pair = 0) {
  const A = await client(11 + pair * 2)
  const B = await client(22 + pair * 2, corruptAt)

  // **The trick this harness stands on**: two instances of the module, each
  // with its own session. If the second instantiation had simply taken over the
  // first, both ends would be the same sim — and every assertion below would
  // pass for the worst possible reason.
  A.advance(0, 0)
  expect([A.frame(), B.frame()]).toEqual([1, 0])
  B.advance(0, 0)

  vi.useFakeTimers()

  let netA!: ReturnType<typeof createNetplay>
  let netB!: ReturnType<typeof createNetplay>
  // Each end impairs what arrives at it, which is where a real link sits.
  // Different seeds, or both directions drop the same packets in lockstep.
  const toB = createLink((d) => netB.receive(d), CONDITIONS, 101)
  const toA = createLink((d) => netA.receive(d), CONDITIONS, 202)

  netA = createNetplay(A, (d) => toB.receive(d), 0)
  netB = createNetplay(B, (d) => toA.receive(d), 1)


  for (let f = 0; f < FRAMES; f++) {
    // Each end presses for its own seat, from its own state, and the other
    // end learns about it over the wire — which is the point.
    netA.step(A.press(0))
    netB.step(B.press(1))
    if (f % 30 === 0) {
      netA.ping()
      netB.ping()
    }
    vi.advanceTimersByTime(STEP_MS)
  }

  // Let everything in flight land and the last corrections settle.
  vi.advanceTimersByTime(2000)

  return {A, B, netA, netB}
}

test('two bots play a match over a bad connection without desyncing', async () => {
  for (let pair = 0; pair < MATCHES; pair++) await oneMatch(pair)
})

async function oneMatch(pair: number) {
  const {A, B, netA, netB} = await botMatch(-1, pair)

  // **The assertion the harness exists for.** The drivers exchange state
  // hashes every 30 frames and count the ones that disagree; with two real
  // sims, a nonzero count is a desync in the simulation itself.
  expect(netA.stats.desyncs).toBe(0)
  expect(netB.stats.desyncs).toBe(0)

  // The netcode was actually exercised: a run with no rollbacks proves the
  // conditions never bit, not that the sim agrees under them.
  expect(netA.stats.rollbacks).toBeGreaterThan(0)
  expect(netB.stats.rollbacks).toBeGreaterThan(0)
  // A correction older than the window cannot be applied, and one arriving is
  // the first sign the rollback parameters do not fit these conditions.
  expect(netA.stats.dropped).toBe(0)
  expect(netB.stats.dropped).toBe(0)

  // Stalling sometimes is the design working; stalling constantly is not.
  expect(netA.stats.frames).toBeGreaterThan(FRAMES * 0.8)

  // Both ends run to the same frame, then hash the whole state. The checksum
  // exchange above samples every 30th frame; this is the two states compared
  // in full, at the end, with nothing left in flight.
  while (A.frame() !== B.frame()) {
    const behind = A.frame() < B.frame() ? netA : netB
    const before = behind === netA ? A.frame() : B.frame()
    behind.step(0)
    if ((behind === netA ? A.frame() : B.frame()) === before) break // stalled: stop
    vi.advanceTimersByTime(STEP_MS)
  }
  expect(A.frame()).toBe(B.frame())
  expect(A.checksum()).toBe(B.checksum())

  // The bots played rather than stood still. A match where nobody pressed
  // anything agrees trivially and is exactly the run that proves nothing, so
  // this asserts damage was dealt — buttons pressed *and* connected.
  expect(A.frame()).toBeGreaterThan(MAX_ROLLBACK)
  expect(netA.log.some((v) => v !== 0)).toBe(true)
  expect(dealt(A).some((d) => d > 0)).toBe(true)
  // The run covered a round transition rather than merely being long enough to
  // have covered one — the comment on FRAMES is a claim, and this is the check.
  expect(round(A)).toBeGreaterThan(1)
}

/**
 * **The detector has to be shown to fire.** A harness that reports no desync is
 * worth exactly what its ability to report one is worth, and the failure mode
 * of a test like this is passing because nothing is connected — two sims that
 * cannot diverge because they are the same sim, a checksum exchange that never
 * happens, an assertion on a counter nothing increments.
 *
 * One end simulates one frame with the wrong remote input, which is what a
 * desync is from the inside.
 *
 * **It is caught by the exchange and not by the final state**, and that is the
 * lesson this test paid for. A flipped input bit that changes no gameplay still
 * changes the state, because the input history ring is *in* the state and the
 * checksum covers it — and then it ages out of the ring 32 frames later and the
 * two states agree again. Comparing the ends only at the end would have missed
 * it entirely. See the cadence check below.
 */
test('a desync on one end is caught', async () => {
  const {netA, netB} = await botMatch(300)

  expect(netA.stats.desyncs + netB.stats.desyncs).toBeGreaterThan(0)
})

/**
 * The coupling the test above uncovered, asserted so it cannot be tuned away by
 * accident: **the checksum cadence must be shorter than the input history**
 * (`InputHistory`, sim/input.go, 32 frames). The ring is the part of the state
 * that forgets, so a divergence confined to it lives for exactly that long — an
 * exchange slower than the ring can sample both sides before and after and see
 * two matching hashes with a desync in between.
 */
test('the checksum cadence samples faster than the input ring forgets', () => {
  expect(CHECKSUM_EVERY).toBeLessThanOrEqual(32)
})
