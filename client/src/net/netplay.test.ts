import {describe, expect, test} from 'vitest'
import {createNetplay, depthP99, INPUT_DELAY, MAX_ROLLBACK, type SimBridge} from './netplay'
import {decodeInputs, decodePing, encodeInputs, encodePing, REDUNDANCY, stampNow} from './packet'

/**
 * A stand-in for the sim that records what it was fed. `applied[f]` holds the
 * *last* inputs simulated for frame f, so after a replay it holds the corrected
 * ones — which is exactly the property the rollback tests care about.
 */
function stubSim() {
  const applied: [number, number][] = []
  let frame = 0
  const sim: SimBridge & {applied: typeof applied; readonly frame: number} = {
    applied,
    get frame() {
      return frame
    },
    advance(p1, p2) {
      applied[frame] = [p1, p2]
      frame++
    },
    rewind(to) {
      if (to >= frame || frame - to > MAX_ROLLBACK) return false
      frame = to
      return true
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
