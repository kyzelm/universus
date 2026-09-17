import {useEffect, useRef, useState} from 'react'
import {createBot} from './game/bot'
import {DUMMY_MODES} from './game/dummy'
import {AI_TIERS} from './sim/wasm'
import {startGame, type Game} from './game/game'
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

/**
 * ?ai, or ?ai=easy|normal|hard. Vs-AI is the mode that demos with no network
 * and no second machine (03 Game Design/AI Opponent.md), so it is reachable
 * from the URL like everything else a run is set up with. Read in two places —
 * the match setup and the control hint — so it is one function.
 */
function aiFromURL(): number {
  const params = new URLSearchParams(location.search)
  if (!params.has('ai')) return 0
  const tier = AI_TIERS.indexOf((params.get('ai') || 'normal') as (typeof AI_TIERS)[number])
  return tier > 0 ? tier : AI_TIERS.indexOf('normal')
}

/**
 * ?p1=0&p2=1 picks the roster entries, defaulting to the first for both.
 *
 * **Not a character select screen**, which belongs with the menus — this is the
 * same thing every other part of a run's setup already is, a URL parameter, and
 * it is what makes the second character playable at all. Both ends of a netplay
 * session must pass the same pair: the characters are in `GameState`, so two
 * clients that disagreed would desync on frame 0 rather than play a mismatch.
 */
function charsFromURL(): [number, number] {
  const params = new URLSearchParams(location.search)
  const pick = (key: string) => {
    const n = Number(params.get(key))
    return Number.isInteger(n) && n >= 0 ? n : 0
  }
  return [pick('p1'), pick('p2')]
}

export default function App() {
  const ai = aiFromURL()
  const host = useRef<HTMLDivElement>(null)
  const [game, setGame] = useState<Game | null>(null)

  useEffect(() => {
    // ?bench leaves the sim untouched so a measurement harness can drive it
    // without the render loop advancing frames underneath it.
    if (new URLSearchParams(location.search).has('bench')) return

    // ?bot hands the local player to a seeded input device, which is what a
    // measurement run needs: rollback only happens when a prediction is wrong,
    // and nobody pressing anything is a prediction that is always right.
    const params = new URLSearchParams(location.search)
    const bot = params.has('bot') ? createBot() : undefined
    // ?training is the lab (03 Game Design/Game Modes.md): infinite resources,
    // no clock, no round end, and no netplay — the mode is sim state, so a
    // match cannot be half in it.
    const training = params.has('training')
    const ai = aiFromURL()
    const chars = charsFromURL()

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

    void startGame(host.current!, bot, training, ai, chars).then((g) => {
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
  }, [])

  return (
    <main>
      <div ref={host} />

      <div className="bar">
        <span className="keys">
          P1 <b>WASD</b> · P2 <b>{ai ? AI_TIERS[ai] : 'arrows'}</b>
          {game?.training && ' · training: R resets · 0-7 dummy (6 record, 7 play)'}
        </span>
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
