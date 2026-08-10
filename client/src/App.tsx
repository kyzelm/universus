import {useEffect, useRef, useState} from 'react'
import {startGame, type Game} from './game/game'
import NetPanel from './net/NetPanel'

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
      <p className="keys">P1 WASD · P2 arrows</p>
      <NetPanel game={game} />
    </main>
  )
}
