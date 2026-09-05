import {afterEach, describe, expect, test, vi} from 'vitest'
import {createLink} from './impair'
import {
  CHECKSUM_EVERY,
  createNetplay,
  depthP99,
  INPUT_DELAY,
  MAX_ROLLBACK,
  type SimBridge,
} from './netplay'
import {
  PacketType,
  REDUNDANCY,
  decodeInputs,
  decodePing,
  encodeChecksum,
  encodeControl,
  encodeInputs,
  encodePing,
  packetType,
  stampNow,
} from './packet'

/**
 * Two drivers wired to each other through a delay queue — the closest thing to
 * a real match that runs headlessly. Seat 0 feeds the sim (mine, theirs) and
 * seat 1 feeds it (theirs, mine), so both stubs see the same argument order and
 * must agree on every hash.
 */
function match(frames: number, lagFrames = 3, corruptBAt = -1) {
  const simA = stubSim()
  const simB = stubSim(corruptBAt)
  const flight: {at: number; toA: boolean; data: ArrayBuffer}[] = []
  let now = 0

  const A = createNetplay(simA, (d) => flight.push({at: now + lagFrames, toA: false, data: d}), 0)
  const B = createNetplay(simB, (d) => flight.push({at: now + lagFrames, toA: true, data: d}), 1)

  for (now = 0; now < frames; now++) {
    for (const p of flight.filter((p) => p.at === now)) (p.toA ? A : B).receive(p.data)
    A.step(now & 0xf)
    B.step((now * 3) & 0xf)
  }
  return {A, B, simA, simB}
}

/**
 * A stand-in for the sim that records what it was fed. `applied[f]` holds the
 * *last* inputs simulated for frame f, so after a replay it holds the corrected
 * ones — which is exactly the property the rollback tests care about.
 */
function stubSim(corruptAt = -1, version = 0xda7a) {
  const applied: [number, number][] = []
  let frame = 0
  const sim: SimBridge & {applied: typeof applied; readonly frame: number} = {
    applied,
    dataVersion: () => version,
    get frame() {
      return frame
    },
    advance(p1, p2) {
      // corruptAt makes this end diverge from the frame given onward, which is
      // what a real desync looks like: one bad frame poisons every hash after.
      applied[frame] = [p1, frame === corruptAt ? p2 ^ 1 : p2]
      frame++
    },
    rewind(to) {
      if (to >= frame || frame - to > MAX_ROLLBACK) return false
      frame = to
      return true
    },
    // FNV-1a over the whole history, so the hash depends on every frame the
    // way the real state hash does.
    checksum() {
      let h = 2166136261
      for (let f = 0; f < frame; f++) {
        for (const v of applied[f]) {
          h = Math.imul(h ^ v, 16777619)
        }
      }
      return h >>> 0
    },
  }
  return sim
}

function harness(seat: 0 | 1 = 0) {
  const sim = stubSim()
  const sent: ArrayBuffer[] = []
  return {sim, sent, net: createNetplay(sim, (d) => sent.push(d), seat)}
}

test('local input lands INPUT_DELAY frames later', () => {
  const {sim, net} = harness()
  net.step(0xa)
  net.step(0xb)
  net.step(0xc)

  // Frames 0 and 1 were already committed before anything was pressed.
  expect(sim.applied[0][0]).toBe(0)
  expect(sim.applied[1][0]).toBe(0)
  expect(sim.applied[INPUT_DELAY][0]).toBe(0xa)
})

test('every packet carries the redundancy window', () => {
  const {sent, net} = harness()
  for (let f = 0; f < 6; f++) net.step(f)

  const first = decodeInputs(sent[0])
  expect(first.startFrame).toBe(0)
  expect(first.inputs.length).toBe(INPUT_DELAY + 1) // no history to send yet

  const last = decodeInputs(sent[sent.length - 1])
  expect(last.inputs.length).toBe(REDUNDANCY)
  expect(last.startFrame + last.inputs.length - 1).toBe(5 + INPUT_DELAY)
})

test('prediction repeats the last input actually seen', () => {
  const {sim, net} = harness()
  net.receive(encodeInputs(0, [5, 5]))
  net.step(0)
  net.step(0)
  net.step(0) // frame 2 is unknown, so it should reuse frame 1's input

  expect(sim.applied[2][1]).toBe(5)
  expect(net.stats.predicted).toBe(1)
})

test('a prediction that turns out right costs nothing', () => {
  const {net} = harness()
  for (let f = 0; f < 4; f++) net.step(0)
  net.receive(encodeInputs(0, [0, 0, 0, 0]))

  expect(net.stats.mispredicted).toBe(0)
  expect(net.stats.rollbacks).toBe(0)
})

test('a wrong prediction rolls back once and replays every frame after it', () => {
  const {sim, net} = harness()
  for (let f = 0; f < 4; f++) net.step(0)
  expect(sim.applied.map((a) => a[1])).toEqual([0, 0, 0, 0])

  // One packet invalidates all four frames at once.
  net.receive(encodeInputs(0, [9, 9, 9, 9]))

  expect(net.stats.rollbacks).toBe(1) // one rollback, not four
  expect(net.stats.mispredicted).toBe(4)
  expect(net.stats.depths[4]).toBe(1)
  expect(sim.applied.map((a) => a[1])).toEqual([9, 9, 9, 9])
  expect(sim.frame).toBe(4) // replayed back up to where it was
})

test('a rollback only rewinds as far as the earliest wrong frame', () => {
  const {sim, net} = harness()
  net.receive(encodeInputs(0, [7, 7]))
  for (let f = 0; f < 5; f++) net.step(0)

  // Frames 0-1 were right; 2 onward were predicted as 7 and were not.
  net.receive(encodeInputs(2, [3, 3, 3]))

  expect(net.stats.depths[3]).toBe(1)
  expect(sim.applied.map((a) => a[1])).toEqual([7, 7, 3, 3, 3])
})

test('seat 1 drives the second player, same argument order', () => {
  const {sim, net} = harness(1)
  net.step(0xa)
  net.step(0xb)
  net.step(0xc)

  expect(sim.applied[INPUT_DELAY][1]).toBe(0xa)
  expect(sim.applied[INPUT_DELAY][0]).toBe(0) // remote is P1 now
})

test('stalls rather than outrunning the rollback window', () => {
  const {sent, net} = harness()

  let simulated = 0
  for (let f = 0; f < 20; f++) if (net.step(0)) simulated++

  expect(simulated).toBe(MAX_ROLLBACK)
  expect(net.stats.stalls).toBe(20 - MAX_ROLLBACK)
  expect(net.frame).toBe(MAX_ROLLBACK)

  // A stalled frame still sends. Waiting silently would leave the other end
  // with nothing to catch up on, and the stall would never end.
  expect(sent.length).toBe(20)
})

describe('depthP99', () => {
  test('is 0 when nothing has rolled back', () => {
    expect(depthP99([0, 0, 0])).toBe(0)
  })

  test('reports the tail, not the bulk', () => {
    const depths = Array(9).fill(0)
    depths[1] = 990
    depths[8] = 10 // 1% of the frames rolled back all the way
    expect(depthP99(depths)).toBe(8)
  })

  test('ignores a tail thinner than 1%', () => {
    const depths = Array(9).fill(0)
    depths[1] = 10_000
    depths[8] = 1
    expect(depthP99(depths)).toBe(1)
  })
})

describe('ping', () => {
  test('is answered with a reply carrying the sender stamp back', () => {
    const {sent, net} = harness()
    net.receive(encodePing(1234, false))

    const reply = decodePing(sent[0])
    expect(reply).toEqual({stamp: 1234, reply: true})
  })

  test('a reply records a round trip in tenths of a ms', () => {
    const {net} = harness()
    net.receive(encodePing(stampNow() - 25, true)) // stamped 2.5 ms ago

    // A range, not an equality: real time passes between the two stamps, and a
    // test that pins the wall clock to a tenth of a ms fails on a slow machine.
    expect(net.stats.rtt.count).toBe(1)
    expect(net.stats.rtt.percentile(50)).toBeGreaterThanOrEqual(2.5)
    expect(net.stats.rtt.percentile(50)).toBeLessThan(10)
  })
})

describe('checksum exchange', () => {
  test('two ends running the same inputs verify clean', () => {
    const {A, B} = match(300)

    expect(A.stats.verified).toBeGreaterThan(5)
    expect(B.stats.verified).toBeGreaterThan(5)
    expect(A.stats.desyncs).toBe(0)
    expect(B.stats.desyncs).toBe(0)
    expect(A.stats.rollbacks).toBeGreaterThan(0) // and it did roll back
  })

  test('one bad frame on one end is caught at the next checkpoint', () => {
    const {A, B} = match(300, 3, 40)

    expect(A.stats.desyncs).toBeGreaterThan(0)
    expect(B.stats.desyncs).toBeGreaterThan(0)
    // Frame 30 hashes frames 0-29 and is still clean; 60 is the first to cover
    // frame 40, so that is where it must surface.
    expect(A.stats.desyncFrame).toBe(60)
  })

  test('a checksum for an unsettled frame waits, then is judged', () => {
    const {net} = harness()

    net.receive(encodeChecksum(30, 0xdeadbeef))
    expect(net.stats.verified).toBe(0)
    expect(net.stats.desyncs).toBe(0) // nothing to compare against yet

    for (let f = 0; f < 40; f++) {
      net.receive(encodeInputs(f, [0]))
      net.step(0)
    }

    // Frame 30 is settled now, so the held checksum finally gets judged — and
    // 0xdeadbeef was never going to match.
    expect(net.stats.desyncs).toBe(1)
    expect(net.stats.desyncFrame).toBe(30)
  })

  test('checkpoints go out only for frames that can no longer change', () => {
    const {sent, net} = harness()
    for (let f = 0; f < 40; f++) {
      net.receive(encodeInputs(f, [0]))
      net.step(0)
    }

    const sums = sent.filter((d) => new DataView(d).getUint8(0) === PacketType.checksum)
    expect(sums.length).toBeGreaterThan(0)
    for (const d of sums) {
      const frame = new DataView(d).getUint32(1, true)
      expect(frame % CHECKSUM_EVERY).toBe(0)
      expect(frame).toBeLessThanOrEqual(net.frame)
    }
  })
})

test('a malformed packet is counted and dropped, never thrown', () => {
  const {net} = harness()
  expect(() => net.receive(new ArrayBuffer(3))).not.toThrow()
  expect(net.stats.malformed).toBe(1)
})

/**
 * The property the whole design rests on: however late the remote inputs
 * arrive, once they have arrived every frame holds the input that really
 * happened. If this passes, the two machines agree.
 */
test('converges on the true remote inputs under lagged delivery', () => {
  const {sim, net} = harness()
  const truth = Array.from({length: 60}, (_, f) => (f * 13) % 7)
  const LAG = 3

  for (let f = 0; f < 60; f++) {
    const arrived = f - LAG
    if (arrived >= 0) {
      const start = Math.max(0, arrived - REDUNDANCY + 1)
      net.receive(encodeInputs(start, truth.slice(start, arrived + 1)))
    }
    net.step(f & 0xf)
  }

  // Everything delivered is settled; the last LAG frames are still guesses.
  for (let f = 0; f <= 60 - 1 - LAG; f++) {
    expect(sim.applied[f][1], `frame ${f}`).toBe(truth[f])
  }
  expect(net.stats.dropped).toBe(0) // the stall guard means this must never fire
  expect(net.stats.rollbacks).toBeGreaterThan(0) // and it did exercise rollback
})

// Character data is part of the simulation's identity. Two clients with
// different frame data produce different states from identical inputs, and no
// amount of checksum exchange can say why after the fact — so it is compared
// once, before the first frame, and a mismatch refuses the match.
test('a peer with different character data is refused, not played', () => {
  const mine = stubSim(-1, 0xaaaa)
  const sent: ArrayBuffer[] = []
  const net = createNetplay(mine, (d) => sent.push(d), 0)

  net.hello()
  expect(sent.length).toBe(1)
  expect(packetType(sent[0])).toBe(PacketType.control)

  net.receive(encodeControl(0xbbbb))
  expect(net.stats.dataMismatch).toBe(true)

  // And nothing is simulated against it.
  expect(net.step(0)).toBe(false)
  expect(mine.applied.length).toBe(0)
})

test('a peer with matching character data plays normally', () => {
  const mine = stubSim(-1, 0xaaaa)
  const net = createNetplay(mine, () => {}, 0)

  net.receive(encodeControl(0xaaaa))
  expect(net.stats.dataMismatch).toBe(false)
  expect(net.step(0)).toBe(true)
})

/**
 * The netcode driven through the real impairment layer, which is the condition
 * it exists for: localhost produces one-frame rollbacks and never exercises the
 * window. 100 ms round trip, 15 ms of jitter and 5% loss is a cell of the
 * measurement matrix (01 Thesis/Measurement Methodology.md, M-B), and the two
 * ends must still agree on every frame they both simulated.
 *
 * Timers are faked, so the run is a few milliseconds of real time and the
 * seeded layer drops exactly the same packets every time it runs.
 */
describe('under artificial network conditions', () => {
  afterEach(() => vi.useRealTimers())

  test('two ends stay identical through latency, jitter and loss', () => {
    vi.useFakeTimers()

    const STEP_MS = 1000 / 60
    const cfg = {delayMs: 50, jitterMs: 15, lossPercent: 5}

    const simA = stubSim()
    const simB = stubSim()

    // Each end's link impairs what *arrives* at it, which is where the real one
    // sits. Different seeds, or both directions would suffer in lockstep.
    let A!: ReturnType<typeof createNetplay>
    let B!: ReturnType<typeof createNetplay>
    const toB = createLink((d) => B.receive(d), cfg, 11)
    const toA = createLink((d) => A.receive(d), cfg, 22)

    A = createNetplay(simA, (d) => toB.receive(d), 0)
    B = createNetplay(simB, (d) => toA.receive(d), 1)

    for (let f = 0; f < 600; f++) {
      A.step(f & 0xf)
      B.step((f * 3) & 0xf)
      vi.advanceTimersByTime(STEP_MS)
    }
    // Let everything still in flight land, then settle the last corrections.
    vi.advanceTimersByTime(1000)

    const shared = Math.min(simA.frame, simB.frame)
    expect(shared).toBeGreaterThan(400) // it stalled sometimes, not constantly
    expect(simA.applied.slice(0, shared)).toEqual(simB.applied.slice(0, shared))

    // The point of the exercise: this is the path localhost never takes.
    expect(A.stats.rollbacks).toBeGreaterThan(0)
    expect(A.stats.desyncs).toBe(0)
    expect(B.stats.desyncs).toBe(0)
    // A correction older than the window cannot be applied, and one arriving
    // is the first sign the parameters do not fit the conditions.
    expect(A.stats.dropped).toBe(0)
    expect(B.stats.dropped).toBe(0)
  })
})
