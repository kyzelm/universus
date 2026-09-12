import {useEffect, useRef, useState} from 'react'
import type {Game} from '../game/game'
import {CLEAN, type Impairment} from './impair'
import {guest, host, type Connection} from './peer'
import {joinRoom, type Kind, type Session} from './room'

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
 * Two ways in. **A room code** goes through our own server, which pairs the two
 * ends, carries the blobs for them and becomes the transport if WebRTC cannot
 * connect (Decision Log D15). **Manual signalling** — copy the blob across,
 * paste the one that comes back — needs no server at all, which is why it stays:
 * a playtest should not be blocked on the backend being up.
 *
 * Once the channel opens this hands the peer to the game and gets out of the
 * way — the netplay numbers live in the HUD, next to the frame they describe.
 */
export default function NetPanel({game}: {game: Game | null}) {
  const [role, setRole] = useState<'idle' | 'host' | 'guest'>('idle')
  const [localBlob, setLocalBlob] = useState('')
  const [remoteBlob, setRemoteBlob] = useState('')
  const [status, setStatus] = useState('not connected')
  const [kind, setKind] = useState<Kind | null>(null)
  const [code, setCode] = useState('')
  const [cfg, setCfg] = useState<Impairment>(CLEAN)

  const conn = useRef<Connection | null>(null)
  const room = useRef<Session | null>(null)

  useEffect(
    () => () => {
      conn.current?.close()
      room.current?.close()
    },
    [],
  )

  /**
   * The room does the whole negotiation, so there is nothing to paste and
   * nothing to time: it comes back with a peer and which seat this end has.
   */
  async function connectRoom() {
    if (!game) return setStatus('the sim is still loading')

    try {
      setStatus('waiting for the other end…')
      const s = await joinRoom(code, (data) => game.receive(data))

      room.current = s
      setRole(s.seat === 0 ? 'host' : 'guest')
      setKind(s.kind)
      game.connect(s.peer, s.seat)
      setStatus('connected')
    } catch (e) {
      setStatus(`failed: ${(e as Error).message}`)
    }
  }

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
          <input
            placeholder="room code"
            value={code}
            // The server takes [A-Za-z0-9-]{1,32} and refuses the rest, so a
            // typed space becomes no character rather than a failed connection.
            onChange={(e) => setCode(e.target.value.replace(/[^A-Za-z0-9-]/g, '').slice(0, 32))}
          />
          <button disabled={!code} onClick={() => void connectRoom()}>
            connect
          </button>
        </div>

        <div className="row">
          <button onClick={() => void connect('host')}>host</button>
          <button onClick={() => setRole('guest')}>join</button>
        </div>

        <p className="keys">
          {status} — same room code on both ends, or <b>host</b>/<b>join</b> to signal by hand
        </p>
      </section>
    )
  }

  const connected = status === 'connected'
  return (
    <section className="net">
      <p className="keys">
        {role} · {status} {kind && `· ${kind}`} {connected && '· WASD moves you'}
      </p>

      {role === 'guest' && !localBlob && !kind && (
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
