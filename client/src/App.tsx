import {useEffect, useRef, useState} from 'react'
import {startGame, type Game} from './game/game'
import NetPanel from './net/NetPanel'

/**
 * Writes the session's inputs out in the replay log format, for testdata/.
 * Every playtest is a free regression log and the corpus is the point.
 */
function saveLog(game: Game) {
  const url = URL.createObjectURL(game.inputLog())
  const a = document.createElement('a')
  a.href = url
  a.download = 'playtest.inputs'
  a.click()
  URL.revokeObjectURL(url)
}

/**
 * The key list, folded away in a <details>. It is read once and then never
 * again, and left open it is a wall of text wider than the game it explains.
 */
function Controls() {
  return (
    <details className="controls">
      <summary>controls</summary>
      <dl>
        <dt>move</dt>
        <dd>
          P1 <b>W A S D</b> · P2 <b>← ↓ ↑ →</b>
        </dd>

        <dt>punch</dt>
        <dd>
          <b>U I O</b> / <b>Num 7 8 9</b> — light, medium, heavy
        </dd>

        <dt>kick</dt>
        <dd>
          <b>J K L</b> / <b>Num 4 5 6</b> — all six work standing, crouching and in the air
        </dd>

        <dt>fireball</dt>
        <dd>↓ ↘ → + punch, three strengths</dd>

        <dt>uppercut</dt>
        <dd>
          → ↓ ↘ + punch, three strengths — or <b>→ ↓ →</b>, the same move without the
          diagonal
        </dd>

        <dt>supers</dt>
        <dd>
          ↓↘→ ×2 + <b>LP</b> (level 1) · ↓↘→ ×2 + <b>LK</b> (level 2) · ↓↙← ×2 + <b>LP</b>{' '}
          (level 3), once the meter has the bars
        </dd>

        <dt>throw</dt>
        <dd>
          <b>H</b> / <b>Num 0</b> / pad <b>L1</b>, or <b>LP+LK</b> on the same frame — beats
          blocking, misses anyone airborne, escaped by pressing throw back within five frames
        </dd>
      </dl>
    </details>
  )
}

export default function App() {
  const host = useRef<HTMLDivElement>(null)
  const [game, setGame] = useState<Game | null>(null)

  useEffect(() => {
    // ?bench leaves the sim untouched so a measurement harness can drive it
    // without the render loop advancing frames underneath it.
    if (new URLSearchParams(location.search).has('bench')) return

    let started: Game | undefined
    let cancelled = false

    // StrictMode mounts twice in dev, and startGame is async: the first run can
    // still be in flight when the cleanup fires.
    void startGame(host.current!).then((g) => {
      if (cancelled) return g.dispose()
      started = g
      setGame(g)
    })

    return () => {
      cancelled = true
      started?.dispose()
      setGame(null)
    }
  }, [])

  return (
    <main>
      <div ref={host} />

      <div className="bar">
        <span className="keys">
          P1 <b>WASD</b> · P2 <b>arrows</b>
        </span>
        <button type="button" onClick={() => game && saveLog(game)} disabled={!game}>
          save input log
        </button>
      </div>

      <Controls />
      <NetPanel game={game} />
    </main>
  )
}
