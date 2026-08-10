// Measures the sim in WASM against the two M0 pass criteria: one frame under
// 0.5 ms, and one frame plus a full-depth rollback under 4 ms.
//
// Percentiles, never bare means — a mean hides exactly the stalls that drop a
// frame. The bars are checked against p99.
//
// Usage: node tools/bench.mjs [samples]
import {execFileSync} from 'node:child_process'
import {mkdtempSync, rmSync} from 'node:fs'
import {tmpdir} from 'node:os'
import {join} from 'node:path'
import {fileURLToPath} from 'node:url'
import {loadSim} from './wasm-load.mjs'

const root = fileURLToPath(new URL('..', import.meta.url))
const samples = Number(process.argv[2] ?? 200)

// Build here rather than trusting client/public/main.wasm, which goes stale the
// moment the sim changes and would quietly benchmark the wrong code.
const dir = mkdtempSync(join(tmpdir(), 'universus-bench-'))
let sim
try {
  const wasm = join(dir, 'main.wasm')
  execFileSync('go', ['build', '-o', wasm, './sim/wasm'], {
    cwd: root,
    env: {...process.env, GOOS: 'js', GOARCH: 'wasm'},
    stdio: 'inherit',
  })
  sim = await loadSim(wasm)
  report(sim, samples)
} finally {
  rmSync(dir, {recursive: true, force: true})
}

// quit() ends Go's main, but the runtime leaves a timer pending that resumes an
// exited program and throws. Exiting in the same tick means it never fires.
sim.quit()
process.exit(process.exitCode ?? 0)

function report(sim, samples) {
  // A single call is far below timer resolution, so each sample times a batch
  // and divides. The distribution is therefore over batches, not over frames —
  // it catches sustained stalls (GC, deopt), not one-off spikes.
  const results = [
    measure('boundary crossing', null, 500, samples, () => sim.noop()),
    measure('sim step', 0.5, 500, samples, advanceOnly(sim)),
    measure('frame + 8-frame rollback', 4, 100, samples, advanceWithRollback(sim)),
  ]

  console.log(`node ${process.version}, ${samples} samples`)
  console.log('op                        p50 ms    p99 ms    max ms   bar')
  let failed = false
  for (const r of results) {
    let verdict = '' // no bar: a reference number, not a criterion
    if (r.bar !== null) {
      const ok = r.p99 < r.bar
      failed ||= !ok
      verdict = `< ${r.bar}  ${ok ? 'PASS' : 'FAIL'}`
    }
    console.log(`${r.label.padEnd(24)} ${ms(r.p50)} ${ms(r.p99)} ${ms(r.max)}  ${verdict}`)
  }
  if (failed) process.exitCode = 1
}

// Function declarations, not consts: the top-level call above runs before any
// const below it is initialised.
function ms(v) {
  return v.toFixed(4).padStart(9)
}

/** Varied inputs: an all-idle sim takes different branches than a live one. */
function inputAt(i) {
  return [(i * 7) % 13 & 0xf, (i * 11) % 17 & 0xf]
}

function advanceOnly(sim) {
  let i = 0
  return () => {
    const [a, b] = inputAt(i++)
    sim.advance(a, b)
  }
}

/**
 * One frame plus a correction at maximum depth: 9 sim steps and 10 boundary
 * crossings, the worst case that still has to fit in 16.6 ms.
 *
 * The replay is driven from JS, exactly as the net layer drives it — that costs
 * a crossing per replayed frame instead of one per rollback, and this is the
 * number that says whether that is affordable.
 */
function advanceWithRollback(sim) {
  let frame = 0
  sim.reset()
  for (; frame < 16; frame++) sim.advance(0, 0)

  return () => {
    const [a, b] = inputAt(frame)
    sim.advance(a, b)
    frame++

    const to = frame - 8
    if (!sim.rewind(to)) throw new Error(`rewind to frame ${to} rejected at frame ${frame}`)
    for (let f = to; f < frame; f++) {
      const [ra, rb] = inputAt(f)
      sim.advance(ra, rb)
    }
  }
}

function measure(label, bar, batch, samples, op) {
  for (let i = 0; i < batch * 5; i++) op() // warm up the JIT before timing

  const per = []
  for (let s = 0; s < samples; s++) {
    const t0 = performance.now()
    for (let i = 0; i < batch; i++) op()
    per.push((performance.now() - t0) / batch)
  }

  per.sort((x, y) => x - y)
  const at = (p) => per[Math.min(per.length - 1, Math.ceil((p / 100) * per.length) - 1)]
  return {label, bar, p50: at(50), p99: at(99), max: per[per.length - 1]}
}
