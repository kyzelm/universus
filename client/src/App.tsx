import {useEffect, useRef, useState} from 'react'
import {createBot} from './game/bot'
import {DUMMY_MODES} from './game/dummy'
import {AI_TIERS, loadSim, roster as loadRoster} from './sim/wasm'
import {startGame, type Game} from './game/game'
import Menu from './Menu'
import {setupFromURL, type RunSetup} from './setup'
import NetPanel from './net/NetPanel'

declare global {
  interface Window {
    /** Installed while a game is running; see Game.measure. */
    universus?: () => Record<string, unknown>
  }
}

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

        <dt>drive impact</dt>
        <dd>
          <b>B</b> / <b>Num 2</b> / pad <b>L3</b>, or <b>O+L</b> / <b>Num 9+6</b> on the same
          frame — one bar, armoured: it eats one hit on the way out
        </dd>

        <dt>drive parry</dt>
        <dd>
          hold <b>G</b> / <b>Num 1</b> / pad <b>L2</b>, or <b>I+K</b> / <b>Num 8+5</b> — drains
          the gauge while held, absorbs with no blockstun, and pays back for reading the attack
          right
        </dd>

        <dt>drive rush</dt>
        <dd>
          → → out of a parry (one bar), or out of a cancelable normal that has connected
          (three) — a dash you can attack out of
        </dd>

        <dt>drive reversal</dt>
        <dd>
          <b>B</b> (or <b>O+L</b>) while blocking — two bars for an invincible way out of the
          pressure
        </dd>

        <dt>EX special</dt>
        <dd>
          the special's motion + <b>U+I</b> — two bars for a stronger version. None of it
          works in Burnout
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

  // The run being played, or null for the menu. A URL that names one skips the
  // menu entirely; a bare URL is somebody arriving, and they get the title.
  const [setup, setSetup] = useState<RunSetup | null>(setupFromURL)

  // The roster for the character select, loaded once. loadSim is idempotent, so
  // this is the same instance startGame goes on to use rather than a second one.
  const [roster, setRoster] = useState<{index: number; name: string}[]>([])
  useEffect(() => {
    let cancelled = false
    void loadSim().then(() => {
      if (!cancelled) setRoster(loadRoster())
    })
    return () => {
      cancelled = true
    }
  }, [])

  const ai = setup?.ai ?? 0

  useEffect(() => {
    if (setup === null) return

    // ?bench leaves the sim untouched so a measurement harness can drive it
    // without the render loop advancing frames underneath it.
    if (new URLSearchParams(location.search).has('bench')) return

    // ?bot hands the local player to a seeded input device, which is what a
    // measurement run needs: rollback only happens when a prediction is wrong,
    // and nobody pressing anything is a prediction that is always right.
    const params = new URLSearchParams(location.search)
    const bot = params.has('bot') ? createBot() : undefined
    // Training is the lab (03 Game Design/Game Modes.md): infinite resources,
    // no clock, no round end, and no netplay — the mode is sim state, so a
    // match cannot be half in it.
    const training = setup.mode === 'training'
    // The box overlay is the lab's whole job (03 Game Design/Game Modes.md),
    // so training gets it without asking; ?boxes turns it on anywhere else,
    // which is what debugging a netplay session needs.
    const showBoxes = training || params.has('boxes')
    const {ai, chars} = setup

    let started: Game | undefined
    let cancelled = false

    // StrictMode mounts twice in dev, and startGame is async: the first run can
    // still be in flight when the cleanup fires.
    // The lab's reset key. It restarts the *match*, which is the same call the
    // app already makes on load — the view is not writing sim state, it is
    // asking for a new one.
    const onKey = (e: KeyboardEvent) => {
      if (!training) return
      if (e.code === 'KeyR') started?.restart()
      // Number row picks what the dummy does; 0 hands seat 2 back to its
      // keyboard. The numpad is that keyboard, so the two never collide.
      const n = /^Digit([0-7])$/.exec(e.code)
      if (n) started?.setDummy(DUMMY_MODES[Number(n[1])])
    }
    window.addEventListener('keydown', onKey)

    void startGame(host.current!, bot, training, ai, chars, showBoxes).then((g) => {
      if (cancelled) return g.dispose()
      started = g
      // The measurement harness reads this. It is installed **here** rather
      // than inside startGame because StrictMode mounts twice in dev: the
      // discarded instance would otherwise be the one answering, and it is
      // the one that never connects to anything.
      window.universus = g.measure
      setGame(g)
    })

    return () => {
      cancelled = true
      window.removeEventListener('keydown', onKey)
      started?.dispose()
      delete window.universus
      setGame(null)
    }
  }, [setup])

  if (setup === null) {
    return (
      <main>
        {roster.length > 0 ? (
          <Menu roster={roster} onStart={setSetup} />
        ) : (
          <p className="keys">loading the sim…</p>
        )}
        <Controls />
      </main>
    )
  }

  return (
    <main>
      <div ref={host} />

      <div className="bar">
        <span className="keys">
          P1 <b>WASD</b> · P2 <b>{ai ? AI_TIERS[ai] : 'arrows'}</b>
          {game?.training && ' · training: R resets · 0-7 dummy (6 record, 7 play)'}
        </span>
        {/*
          Back to the title. Only offered when the URL did not name the run:
          a measurement harness, a CI run and a link somebody was sent all
          arrive with parameters, and none of them has a menu to return to.
        */}
        {setupFromURL() === null && (
          <button type="button" onClick={() => setSetup(null)}>
            menu
          </button>
        )}
        {/*
          Every mode saves now, including the lab and an AI match. The log used
          to be the inputs the *caller* fed the sim and nothing else, which is
          why those two were refused or silently wrong: the AI's presses are
          generated inside the sim and the lab changes what a frame does. The
          header carries the setup, so the log replays the match that was
          played rather than one that resembles it.
        */}
        <button type="button" onClick={() => game && saveLog(game)} disabled={!game}>
          save input log
        </button>
      </div>

      <Controls />
      <NetPanel game={game} />
    </main>
  )
}
