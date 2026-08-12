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
      <p className="keys">
        P1 WASD + UIO/JKL · P2 arrows + numpad{' '}
        <button type="button" onClick={() => game && saveLog(game)} disabled={!game}>
          save input log
        </button>
      </p>
      <NetPanel game={game} />
    </main>
  )
}
