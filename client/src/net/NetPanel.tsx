import {useEffect, useRef, useState} from 'react'
import type {Game} from '../game/game'
import {CLEAN, type Impairment} from './impair'
import {guest, host, type Connection} from './peer'
import {joinQueue, joinRoom, type Kind, type Session} from './room'
import SignIn from './SignIn'
import {account, type Account} from './account'
import {roster} from '../sim/wasm'

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
 * A whole run out of the URL: `?room=CODE&bot&rtt=200&loss=5&jitter=15`.
 *
 * This is what makes a measurement cell repeatable and a two-machine test one
 * link to send rather than a list of instructions — both ends open the same
 * address and are in the same condition by construction. Setting one end by
 * hand is how a cell gets measured with the impairment on one side only, which
 * is not the condition on the label.
 */
function urlImpairment(): Impairment | null {
  const p = new URLSearchParams(location.search)
  if (!p.has('rtt') && !p.has('loss') && !p.has('jitter')) return null

  const num = (key: string) => Math.max(0, Number(p.get(key) ?? 0) || 0)
  // The presets are round-trip figures and the layer delays arrivals in one
  // direction, so it is given half — the same halving the buttons do.
  return {delayMs: num('rtt') / 2, jitterMs: num('jitter'), lossPercent: num('loss')}
}

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
/**
 * "Shoto vs Grappler". Read from the sim rather than kept here, so a roster
 * change cannot leave the panel naming a fighter nobody is playing.
 */
function matchup(chars: [number, number]): string {
  const names = roster()
  return `${names[chars[0]]?.name ?? '?'} vs ${names[chars[1]]?.name ?? '?'}`
}

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
  // Who is signed in, if anybody. Only the queue needs it — a private match by
  // code needs one too (D104), but the server is what enforces that and saying
  // so twice is two places to disagree.
  const [who, setWho] = useState<Account | null>(account)
  const autoJoined = useRef(false)

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
  async function connectRoom(joining = code) {
    if (!game) return setStatus('the sim is still loading')

    try {
      setStatus('waiting for the other end…')
      // This end's pick goes in; the host's comes back, for both ends.
      const s = await joinRoom(joining, (data) => game.receive(data), game.chars)

      room.current = s
      setRole(s.seat === 0 ? 'host' : 'guest')
      setKind(s.kind)
      game.connect(s.peer, s.seat, {chars: s.chars, matchID: s.matchID, transport: s.kind})
      // Naming the pair is not decoration for the guest: the host picked it, so
      // a player who chose one fighter and is handed another needs the line
      // that says why. It costs the host nothing to read the same line.
      setStatus(`connected — ${matchup(s.chars)}`)

      // Applied after connecting, not before: connect() builds the link, so a
      // condition set earlier would be replaced by the clean one.
      const condition = urlImpairment()
      if (condition) impair(condition)
    } catch (e) {
      setStatus(`failed: ${(e as Error).message}`)
    }
  }

  /**
   * The queue. Everything after the pairing is the negotiation a private match
   * already uses, so this differs from connectRoom by one call and by who
   * decided the two of you should meet.
   */
  async function queueUp(mode: 'ranked' | 'casual') {
    if (!game) return setStatus('the sim is still loading')

    try {
      setStatus(`waiting for a ${mode} match…`)
      const s = await joinQueue(mode, (data) => game.receive(data), game.chars)

      room.current = s
      setRole(s.seat === 0 ? 'host' : 'guest')
      setKind(s.kind)
      game.connect(s.peer, s.seat, {chars: s.chars, matchID: s.matchID, transport: s.kind})
      setStatus(`connected — ${matchup(s.chars)}`)

      const condition = urlImpairment()
      if (condition) impair(condition)
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

  // ?room=CODE joins on load. Same link on both ends, nothing typed, which is
  // the only way a two-machine run is set up identically on both sides.
  useEffect(() => {
    const joining = new URLSearchParams(location.search).get('room')
    if (!game || !joining || autoJoined.current) return

    autoJoined.current = true
    setCode(joining)
    void connectRoom(joining)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [game])

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

  // Training is offline by construction: the mode is sim state, so two ends
  // that disagreed about it would desync on the first checksum. Refused here
  // rather than explained after the fact.
  if (game?.training) {
    return (
      <section className="net">
        <p className="keys">
          training — infinite resources, no clock, no rounds. <b>R</b> resets. Reload without{' '}
          <b>?training</b> to play online
        </p>
      </section>
    )
  }

  if (role === 'idle') {
    return (
      <section className="net">
        <SignIn onChange={setWho} />

        {/*
          Ranked and casual are the same socket and the same negotiation; what
          differs is how wide the skill bands open while you wait (D43, D44).
          Both need an account, so they are offered only once there is one —
          a button that always answers "sign in first" is a button that should
          have been a sentence.
        */}
        {who && (
          <div className="row">
            <button onClick={() => void queueUp('ranked')}>ranked queue</button>
            <button onClick={() => void queueUp('casual')}>casual queue</button>
          </div>
        )}

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
          {status} — same room code on both ends, or <b>host</b>/<b>join</b> to signal by hand.
          The host&apos;s characters are the ones played; signalling by hand has no room to
          agree over, so both ends must have picked the same pair
        </p>
      </section>
    )
  }

  const connected = status.startsWith('connected')
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
