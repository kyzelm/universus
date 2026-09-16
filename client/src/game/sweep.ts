import {MAX_ROLLBACK} from '../net/netplay'
import {createBot} from './bot'
import {createSamples} from './stats'

/**
 * M-A, measured deliberately rather than waited for: frame time at forced
 * rollback depths 0 through 8 (01 Thesis/Measurement Methodology.md).
 *
 * **Why forcing beats observing.** The per-depth buckets in the netplay stats
 * report what a real match happened to cost, and a real match decides its own
 * depths — a healthy connection never produces a depth-8 frame at all, so the
 * one cell the 16.6 ms budget is really about is the cell a session is least
 * likely to fill. Here every depth is run on demand, on a fixed input stream,
 * so the table is complete and repeatable.
 *
 * **Why it runs in the browser.** `tools/bench.mjs` measures the same shapes in
 * Node, and M0 recorded them there — same binary, same V8, but no compositor,
 * no page and no rAF. The result chapter's M-A figure has to come from the host
 * the game actually runs in, and this is that harness.
 */
export interface SweepSim {
  /** Restarts the match. Called between batches, never inside a timed one. */
  reset(): void
  advance(p1: number, p2: number): void
  /** Restores the state at the start of `frame`; false is outside the window. */
  rewind(frame: number): boolean
}

export interface SweepRow {
  depth: number
  /** Timed batches. The distribution is over batches, not over single frames. */
  samples: number
  /** Frames per batch. Varies by depth — see `batchFor`. */
  batch: number
  /** Milliseconds per frame at this depth, including the replay it forced. */
  p25: number
  p50: number
  p75: number
  p99: number
  max: number
}

export interface SweepOptions {
  samples?: number
  /** Sim steps a batch should do, before the depth divides it up. */
  stepsPerBatch?: number
  maxDepth?: number
  /** Untimed batches run first, so the JIT is warm before anything counts. */
  warmup?: number
  /** Restart the match this often, so the sweep never times a finished one. */
  resetEvery?: number
}

const DEFAULTS: Required<SweepOptions> = {
  samples: 200,
  stepsPerBatch: 500,
  maxDepth: MAX_ROLLBACK,
  warmup: 5,
  // A round is 99 seconds and a match is best of three, so a long run would
  // otherwise spend most of itself timing a sim sitting in the match-end phase,
  // which simulates nothing and is cheap for the wrong reason.
  resetEvery: 3600,
}

/**
 * One frame at depth `d` costs `d + 1` sim steps, so a fixed frame count per
 * batch would do nine times the work at depth 8 that it does at depth 0 — and
 * the depths would then be timed against different amounts of clock noise.
 * Dividing keeps the work per batch roughly constant instead.
 *
 * A single frame is far below timer resolution (`performance.now` is coarsened
 * deliberately in every browser), which is why anything is batched at all: the
 * distribution catches sustained stalls, GC and deopt, and hides one-off spikes.
 */
export function batchFor(depth: number, stepsPerBatch: number): number {
  return Math.max(1, Math.round(stepsPerBatch / (depth + 1)))
}

/**
 * Runs the sweep, yielding to the event loop often enough that a page can draw
 * its progress. `onDepth` is called before each depth starts.
 */
export async function runSweep(
  sim: SweepSim,
  opts: SweepOptions = {},
  onDepth?: (depth: number) => void,
): Promise<SweepRow[]> {
  const cfg = {...DEFAULTS, ...opts}
  const rows: SweepRow[] = []

  for (let depth = 0; depth <= cfg.maxDepth; depth++) {
    onDepth?.(depth)
    rows.push(await measureDepth(sim, depth, cfg))
  }
  return rows
}

async function measureDepth(
  sim: SweepSim,
  depth: number,
  cfg: Required<SweepOptions>,
): Promise<SweepRow> {
  // The same stream every depth sees, and the same one twice if the sweep is
  // re-run: a measurement that cannot be repeated is an anecdote. Two seeds,
  // because two seats pressing identical buttons is not a match.
  const p1 = createBot(1)
  const p2 = createBot(2)
  const bits: number[] = []
  let frame = 0

  function step(): void {
    const a = p1.poll()
    const b = p2.poll()
    // Kept because the replay has to feed the sim what it fed it the first
    // time. Replaying different inputs would measure a different match, and
    // the rollback the net layer performs replays the frames it already ran.
    bits[frame * 2] = a
    bits[frame * 2 + 1] = b
    sim.advance(a, b)
    frame++
  }

  function restart(): void {
    sim.reset()
    frame = 0
    bits.length = 0
    p1.reseed(1)
    p2.reseed(2)
    // Enough history that the first rewind of the batch has somewhere to land.
    for (let i = 0; i <= cfg.maxDepth; i++) step()
  }

  /** One measured frame: advance, then pay for a correction of this depth. */
  function iteration(): void {
    step()
    if (depth === 0) return

    const to = frame - depth
    if (!sim.rewind(to)) throw new Error(`rewind to frame ${to} rejected at frame ${frame}`)
    for (let f = to; f < frame; f++) sim.advance(bits[f * 2], bits[f * 2 + 1])
  }

  const batch = batchFor(depth, cfg.stepsPerBatch)
  const per = createSamples(cfg.samples)
  restart()

  for (let b = 0; b < cfg.warmup + cfg.samples; b++) {
    // Outside the timed region on purpose: a reset is not part of a frame.
    if (frame >= cfg.resetEvery) restart()

    const t0 = performance.now()
    for (let i = 0; i < batch; i++) iteration()
    const ms = (performance.now() - t0) / batch

    if (b >= cfg.warmup) per.push(ms)
    // Long enough between yields that their overhead is noise, often enough
    // that the page repaints while a nine-depth sweep runs.
    if (b % 20 === 19) await yieldNow()
  }

  return {
    depth,
    samples: per.count,
    batch,
    p25: per.percentile(25),
    p50: per.percentile(50),
    p75: per.percentile(75),
    p99: per.percentile(99),
    max: per.percentile(100),
  }
}

/**
 * Hands the event loop one turn, so the page can paint what depth it is on.
 *
 * A `setTimeout` would be clamped to a second or more the moment the tab stops
 * being visible — the same throttling that gave a hidden guest `frame p50
 * 100 ms` while verifying the HUD (01 Thesis/Measurement Methodology.md). A
 * message port is not clamped, so a sweep left running behind another window
 * finishes in the time the work takes rather than the time the clamp imposes.
 * Nothing timed happens here either way: the batches are measured around a
 * synchronous loop, and this runs between them.
 */
function yieldNow(): Promise<void> {
  return new Promise((resolve) => {
    const {port1, port2} = new MessageChannel()
    port1.onmessage = () => {
      port1.close()
      resolve()
    }
    port2.postMessage(null)
  })
}

/**
 * The rows as a file, with the run's own conditions in the first line.
 *
 * Every chart states its own measurement method, and the reporting rules ask
 * for the host and the trial count beside every figure — a CSV that carries
 * neither becomes an orphan the week after it is saved.
 */
export function toCSV(rows: readonly SweepRow[], note: string): string {
  const head = `# ${note}`
  const cols = 'depth,samples,batch,p25_ms,p50_ms,p75_ms,p99_ms,max_ms'
  const body = rows.map(
    (r) =>
      `${r.depth},${r.samples},${r.batch},` +
      [r.p25, r.p50, r.p75, r.p99, r.max].map((v) => v.toFixed(4)).join(','),
  )
  return [head, cols, ...body].join('\n') + '\n'
}
