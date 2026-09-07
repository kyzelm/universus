import {useEffect, useRef, useState} from 'react'
import type {Game} from '../game/game'
import {CLEAN, type Impairment} from './impair'
import {guest, host, type Connection} from './peer'

/**
 * The measurement matrix, as buttons (01 Thesis/Measurement Methodology.md,
 * M-B): 0/50/100/150/200 ms against 0/2/5% loss. Fifteen cells, run by hand,
 * so they are one click each rather than three numbers typed correctly.
 *
 * The presets are **round-trip** figures because that is how the matrix is
 * labelled and how the HUD reports what it measured. The layer delays packets
 * on arrival, one direction only, so the delay it is given is half the RTT —
 * and **both ends must select the same preset**, or the path is impaired in
 * one direction and the condition is not the one on the label.
 */
const RTT_PRESETS = [0, 50, 100, 150, 200]
const LOSS_PRESETS = [0, 2, 5]

/**
 * Manual signalling for the M0 spike: copy the blob to the other end, paste the
 * one that comes back. The signalling server is M3.
 *
 * Once the channel opens this hands the peer to the game and gets out of the
 * way — the netplay numbers live in the HUD, next to the frame they describe.
 */
export default function NetPanel({game}: {game: Game | null}) {
  const [role, setRole] = useState<'idle' | 'host' | 'guest'>('idle')
  const [localBlob, setLocalBlob] = useState('')
  const [remoteBlob, setRemoteBlob] = useState('')
  const [status, setStatus] = useState('not connected')
  const [cfg, setCfg] = useState<Impairment>(CLEAN)

  const conn = useRef<Connection | null>(null)

  useEffect(() => () => conn.current?.close(), [])

  async function connect(as: 'host' | 'guest') {
    if (!game) return setStatus('the sim is still loading')

    try {
      setStatus('gathering candidates…')
      const forward = (data: ArrayBuffer) => game.receive(data)
      const c = as === 'host' ? await host(forward) : await guest(remoteBlob, forward)

      conn.current = c
      setRole(as)
      setLocalBlob(c.localBlob)
      setStatus(as === 'host' ? 'paste the answer below' : 'send the answer back')

      // The host is seat 0 and drives P1; whoever joined drives P2.
      game.connect(await c.ready, as === 'host' ? 0 : 1)
      setStatus('connected')
    } catch (e) {
      setStatus(`failed: ${(e as Error).message}`)
    }
  }

  /**
   * Conditions are pushed into the game rather than held there: the panel is
   * the only thing that knows what was asked for, and the game is the only
   * thing that can apply it.
   */
  function impair(next: Impairment) {
    setCfg(next)
    game?.impair(next)
  }

  /** The blob came off a clipboard, so a bad paste has to land as a message. */
  async function acceptAnswer() {
    try {
      setStatus('connecting…')
      await conn.current?.accept?.(remoteBlob)
    } catch (e) {
      setStatus(`failed: ${(e as Error).message}`)
    }
  }

  if (role === 'idle') {
    return (
      <section className="net">
        <div className="row">
          <button onClick={() => void connect('host')}>host</button>
          <button onClick={() => setRole('guest')}>join</button>
        </div>
        <p className="keys">{status}</p>
      </section>
    )
  }

  const connected = status === 'connected'
  return (
    <section className="net">
      <p className="keys">
        {role} · {status} {connected && '· WASD moves you'}
      </p>

      {role === 'guest' && !localBlob && (
        <>
          <textarea
            placeholder="paste the host's offer"
            value={remoteBlob}
            onChange={(e) => setRemoteBlob(e.target.value)}
          />
          <button onClick={() => void connect('guest')}>create answer</button>
        </>
      )}

      {localBlob && !connected && (
        <textarea readOnly value={localBlob} onFocus={(e) => e.currentTarget.select()} />
      )}

      {role === 'host' && !connected && (
        <>
          <textarea
            placeholder="paste the guest's answer"
            value={remoteBlob}
            onChange={(e) => setRemoteBlob(e.target.value)}
          />
          <button onClick={() => void acceptAnswer()}>accept answer</button>
        </>
      )}

      {connected && (
        <div className="conditions">
          <p className="keys">
            artificial conditions — set the same pair on <b>both</b> ends, then read the
            measured RTT off the HUD
          </p>

          <div className="row">
            {RTT_PRESETS.map((rtt) => (
              <button
                key={rtt}
                type="button"
                className={cfg.delayMs * 2 === rtt ? 'on' : ''}
                onClick={() => impair({...cfg, delayMs: rtt / 2})}
              >
                {rtt} ms
              </button>
            ))}
          </div>

          <div className="row">
            {LOSS_PRESETS.map((loss) => (
              <button
                key={loss}
                type="button"
                className={cfg.lossPercent === loss ? 'on' : ''}
                onClick={() => impair({...cfg, lossPercent: loss})}
              >
                {loss}% loss
              </button>
            ))}
            <label>
              jitter
              <input
                type="number"
                min={0}
                max={200}
                value={cfg.jitterMs}
                onChange={(e) => impair({...cfg, jitterMs: Math.max(0, +e.target.value)})}
              />
              ms
            </label>
          </div>
        </div>
      )}
    </section>
  )
}
