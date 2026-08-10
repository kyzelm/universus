import {useEffect, useRef} from 'react'
import {startGame} from './game/game'
import NetPanel from './net/NetPanel'

export default function App() {
  const host = useRef<HTMLDivElement>(null)

  useEffect(() => {
    // ?bench leaves the sim untouched so a measurement harness can drive it
    // without the render loop advancing frames underneath it.
    if (new URLSearchParams(location.search).has('bench')) return

    let dispose: (() => void) | undefined
    let cancelled = false

    // StrictMode mounts twice in dev, and startGame is async: the first run can
    // still be in flight when the cleanup fires.
    void startGame(host.current!).then((d) => (cancelled ? d() : (dispose = d)))

    return () => {
      cancelled = true
      dispose?.()
    }
  }, [])

  return (
    <main>
      <div ref={host} />
      <p className="keys">P1 WASD · P2 arrows</p>
      <NetPanel />
    </main>
  )
}