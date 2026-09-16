import {expect, test} from 'vitest'
import {batchFor, runSweep, toCSV, type SweepSim} from './sweep'

const OPTS = {samples: 2, stepsPerBatch: 12, maxDepth: 2, warmup: 0, resetEvery: 1e9}

/**
 * Counts what the sweep asked the sim to do, and keeps a frame number the way
 * the real sim does — a rewind moves it back, and the replay advances it up
 * again. That is what makes the depth of each rewind recoverable here.
 */
function stub() {
  const depths: number[] = []
  let advances = 0
  let resets = 0
  let frame = 0

  const sim: SweepSim = {
    reset() {
      resets++
      frame = 0
    },
    advance() {
      advances++
      frame++
    },
    rewind(to) {
      depths.push(frame - to)
      frame = to
      return true
    },
  }
  return {
    sim,
    depths,
    get advances() {
      return advances
    },
    get resets() {
      return resets
    },
  }
}

test('a frame at depth d runs the sim d+1 times', async () => {
  // The cost model the whole measurement is about: an 8-frame rollback runs the
  // sim nine times and still has to fit one frame. If this drifts, every M-A
  // number drifts with it and nothing else would notice.
  const s = stub()
  const rows = await runSweep(s.sim, OPTS)

  expect(rows.map((r) => r.depth)).toEqual([0, 1, 2])

  let expected = 0
  for (const row of rows) {
    // Each depth restarts the match and warms up maxDepth+1 frames of history.
    expected += OPTS.maxDepth + 1
    expected += row.samples * row.batch * (row.depth + 1)
  }
  expect(s.advances).toBe(expected)
  expect(s.resets).toBe(rows.length)
})

test('every rewind goes back exactly the depth being measured', async () => {
  // A row that rewound one frame while claiming depth 8 would report the cost
  // of the cheapest correction under the name of the most expensive one.
  const s = stub()
  const rows = await runSweep(s.sim, OPTS)

  const counted = new Map<number, number>()
  for (const d of s.depths) counted.set(d, (counted.get(d) ?? 0) + 1)

  const expected = new Map(
    rows.filter((r) => r.depth > 0).map((r) => [r.depth, r.samples * r.batch]),
  )
  expect(counted).toEqual(expected)
})

test('depth 0 never rewinds', async () => {
  const s = stub()
  await runSweep(s.sim, {...OPTS, maxDepth: 0})
  expect(s.depths).toEqual([])
})

test('a refused rewind names the frame rather than reporting a fast run', async () => {
  // Silently skipping the replay would leave a plausible-looking row that
  // measured a plain frame and called it a depth-2 one.
  const sim = {...stub().sim, rewind: () => false}
  await expect(runSweep(sim, OPTS)).rejects.toThrow(/rewind to frame \d+ rejected/)
})

test('batches shrink with depth, so every batch does about the same work', () => {
  expect(batchFor(0, 500)).toBe(500)
  expect(batchFor(1, 500)).toBe(250)
  expect(batchFor(8, 500)).toBe(56)
  expect(batchFor(8, 1)).toBe(1)
})

test('the CSV carries the run conditions with it', () => {
  const csv = toCSV([{depth: 1, samples: 2, batch: 3, p25: 1, p50: 2, p75: 3, p99: 4, max: 5}], 'a note')
  expect(csv.split('\n')[0]).toBe('# a note')
  expect(csv.split('\n')[1]).toBe('depth,samples,batch,p25_ms,p50_ms,p75_ms,p99_ms,max_ms')
  expect(csv.split('\n')[2]).toBe('1,2,3,1.0000,2.0000,3.0000,4.0000,5.0000')
})
