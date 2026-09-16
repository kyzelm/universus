import {useEffect, useState} from 'react'
import {advance, loadSim, reset, rewind} from '../sim/wasm'
import {runSweep, toCSV, type SweepRow} from './sweep'

/**
 * `?sweep` — the M-A page: frame time at forced rollback depths 0 through 8,
 * measured in the browser (01 Thesis/Measurement Methodology.md).
 *
 * It is its own page rather than a key in the game, because it drives the sim
 * itself: the render loop advancing frames underneath it would time something
 * else entirely, exactly as `?bench` avoids.
 */

// The sweep owns the sim for as long as it runs, and StrictMode mounts twice in
// development — two sweeps interleaved on one sim would rewind each other's
// frames and report the cost of the collision.
let running = false

export default function Sweep() {
  const [rows, setRows] = useState<SweepRow[] | null>(null)
  const [at, setAt] = useState(0)
  const [error, setError] = useState('')
  // **A background tab is not a measurement** (01 Thesis/Measurement
  // Methodology.md). The batches here are timed around synchronous work, so a
  // hidden tab does not corrupt them the way it corrupts an rAF loop — but
  // Chrome throttles a hidden tab's timers and eventually freezes it outright,
  // and a number taken across that is not one to put in a chapter. Recorded
  // rather than prevented: the run is what it is, and the file says so.
  const [wasHidden, setWasHidden] = useState(document.hidden)

  useEffect(() => {
    const onVisible = () => document.hidden && setWasHidden(true)
    document.addEventListener('visibilitychange', onVisible)

    if (!running) {
      running = true
      void (async () => {
        try {
          await loadSim()
          reset()
          setRows(await runSweep({reset: () => reset(), advance, rewind}, {}, setAt))
        } catch (e) {
          setError(String(e))
        } finally {
          running = false
        }
      })()
    }
    return () => document.removeEventListener('visibilitychange', onVisible)
  }, [])

  const note =
    `universus M-A sweep · ${new Date().toISOString()} · ${navigator.userAgent}` +
    (wasHidden ? ' · WARNING: the tab was hidden during this run' : '')

  return (
    <main className="sweep">
      <h1>M-A — frame time by rollback depth</h1>

      {error && <p className="error">{error}</p>}
      {!rows && !error && <p>measuring depth {at} of 8…</p>}

      {rows && (
        <>
          <table>
            <thead>
              <tr>
                <th>depth</th>
                <th>sim steps</th>
                <th>batches</th>
                <th>frames/batch</th>
                <th>p25</th>
                <th>p50</th>
                <th>p75</th>
                <th>p99</th>
                <th>max</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((r) => (
                <tr key={r.depth}>
                  <td>{r.depth}</td>
                  {/* What the frame actually ran: a depth-8 correction simulates it nine times. */}
                  <td>{r.depth + 1}</td>
                  <td>{r.samples}</td>
                  <td>{r.batch}</td>
                  <td>{r.p25.toFixed(4)}</td>
                  <td>{r.p50.toFixed(4)}</td>
                  <td>{r.p75.toFixed(4)}</td>
                  <td>{r.p99.toFixed(4)}</td>
                  <td>{r.max.toFixed(4)}</td>
                </tr>
              ))}
            </tbody>
          </table>

          <p>
            Milliseconds per frame, including the replay the depth forced, against a 16.6 ms
            budget. Each figure is a batch mean, so the distribution catches sustained stalls
            and hides one-off spikes — and the display refresh rate does not enter into it,
            because nothing here is driven by the display.
          </p>

          {wasHidden && (
            <p className="error">
              This tab was hidden while the sweep ran. The timing is measured around
              synchronous work so the figures are probably sound, but a throttled tab is not a
              measurement — re-run it in front of you before the numbers go in a chapter.
            </p>
          )}

          <button type="button" onClick={() => download(toCSV(rows, note))}>
            save CSV
          </button>
          <p className="note">{note}</p>
        </>
      )}
    </main>
  )
}

function download(csv: string) {
  const url = URL.createObjectURL(new Blob([csv], {type: 'text/csv'}))
  const a = document.createElement('a')
  a.href = url
  a.download = 'm-a-sweep.csv'
  a.click()
  URL.revokeObjectURL(url)
}
