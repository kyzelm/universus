import {useState} from 'react'
import {AI_TIERS} from './sim/wasm'
import {MODES, type Mode, type RunSetup} from './setup'

const MODE_LABELS: Record<Mode, string> = {
  local: 'local versus',
  ai: 'versus AI',
  training: 'training',
  online: 'online',
}

const MODE_HINTS: Record<Mode, string> = {
  local: 'two players, one machine. The simplest integration test of the whole sim',
  ai: 'a scripted opponent. No network and no second machine',
  training: 'boxes, frame data, a dummy that records and plays back',
  online: 'rollback netcode over a room code. Both players must pick the same pair',
}

export interface MenuProps {
  roster: {index: number; name: string}[]
  onStart(setup: RunSetup): void
}

/**
 * Title, then a character select. Two screens, because a mode and a pair of
 * fighters are the only two decisions a run needs — everything else the game
 * has is either a per-user setting, which belongs in a settings screen nobody
 * has asked for yet, or a URL parameter a measurement run passes.
 *
 * **The URL still starts a run directly** (see App): this screen is what a
 * person gets, and the parameters are what CI, the measurement harness and a
 * shared link get. Neither is a special case of the other, and the menu builds
 * exactly the setup the parameters build.
 */
export default function Menu({roster, onStart}: MenuProps) {
  const [mode, setMode] = useState<Mode | null>(null)
  const [ai, setAI] = useState(AI_TIERS.indexOf('normal'))
  const [chars, setChars] = useState<[number, number]>([0, 0])

  if (mode === null) {
    return (
      <div className="menu">
        <h1>UNIVERSUS</h1>
        <div className="modes">
          {MODES.map((m) => (
            <button key={m} type="button" onClick={() => setMode(m)}>
              <b>{MODE_LABELS[m]}</b>
              <span>{MODE_HINTS[m]}</span>
            </button>
          ))}
        </div>
      </div>
    )
  }

  // Who the second column belongs to is the mode's business, and saying so is
  // most of what makes the screen readable: in training it is the dummy, and
  // against the AI it is the opponent nobody is holding.
  const seat2 =
    mode === 'training' ? 'dummy' : mode === 'ai' ? 'opponent' : mode === 'online' ? 'P2' : 'P2'

  return (
    <div className="menu">
      <h1>{MODE_LABELS[mode]}</h1>

      <div className="select">
        {(['P1', seat2] as const).map((label, seat) => (
          <div key={label} className="column">
            <h2>{label}</h2>
            {roster.map((c) => (
              <button
                key={c.index}
                type="button"
                className={chars[seat] === c.index ? 'on' : ''}
                onClick={() =>
                  setChars((prev) => {
                    const next: [number, number] = [prev[0], prev[1]]
                    next[seat] = c.index
                    return next
                  })
                }
              >
                {c.name}
              </button>
            ))}
          </div>
        ))}
      </div>

      {mode === 'ai' && (
        <div className="row">
          {AI_TIERS.slice(1).map((name, i) => (
            <button
              key={name}
              type="button"
              className={ai === i + 1 ? 'on' : ''}
              onClick={() => setAI(i + 1)}
            >
              {name}
            </button>
          ))}
        </div>
      )}

      <div className="row">
        <button type="button" onClick={() => setMode(null)}>
          back
        </button>
        <button type="button" onClick={() => onStart({mode, ai: mode === 'ai' ? ai : 0, chars})}>
          fight
        </button>
      </div>
    </div>
  )
}
