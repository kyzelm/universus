import {useEffect, useRef, useState} from 'react'
import {decodeInputs, encodeInputs, REDUNDANCY} from './packet'
import {guest, host, type Connection, type Peer} from './peer'

/**
 * Manual signalling and a traffic test for the M0 transport spike: connect two
 * tabs, push an inputs packet per frame, and show what arrives.
 *
 * The traffic is synthetic — this proves the channel, the packet format and the
 * redundancy window carry inputs at 60 Hz. Feeding real inputs into rollback is
 * the next step, and this panel is throwaway either way.
 */
export default function NetPanel() {
  const [role, setRole] = useState<'idle' | 'host' | 'guest'>('idle')
  const [localBlob, setLocalBlob] = useState('')
  const [remoteBlob, setRemoteBlob] = useState('')
  const [status, setStatus] = useState('not connected')
  const [stats, setStats] = useState(zero)

  const conn = useRef<Connection | null>(null)
  const seen = useRef(new Set<number>())
  const live = useRef(zero)

  useEffect(() => () => conn.current?.close(), [])

  function receive(data: ArrayBuffer) {
    let packet
    try {
      packet = decodeInputs(data)
    } catch {
      live.current = {...live.current, malformed: live.current.malformed + 1}
      return
    }

    const {startFrame, inputs} = packet
    const newest = startFrame + inputs.length - 1
    let recovered = live.current.recovered

    for (let i = 0; i < inputs.length; i++) {
      const f = startFrame + i
      if (seen.current.has(f)) continue
      seen.current.add(f)
      // First learned from a redundant slot rather than as the newest input:
      // the packet that carried it as newest never arrived.
      if (f !== newest) recovered++
    }

    live.current = {
      ...live.current,
      recv: live.current.recv + 1,
      lastFrame: Math.max(live.current.lastFrame, newest),
      bytes: data.byteLength,
      recovered,
    }
  }

  async function connect(as: 'host' | 'guest') {
    try {
      setStatus('gathering candidates…')
      const c = as === 'host' ? await host(receive) : await guest(remoteBlob, receive)
      conn.current = c
      setRole(as)
      setLocalBlob(c.localBlob)
      setStatus(as === 'host' ? 'paste the answer below' : 'send the answer back')
      pump(await c.ready)
    } catch (e) {
      setStatus(`failed: ${(e as Error).message}`)
    }
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

  /**
   * One inputs packet per frame, each carrying the last REDUNDANCY frames.
   *
   * Measured: a hidden tab gets 3 interval ticks where 137 are due — Chrome
   * throttles background timers to about 1 Hz, and requestAnimationFrame stops
   * entirely. So a player who tabs away stops sending inputs within a frame or
   * two. M2 has to treat that as a stall like any other, not as a clean
   * disconnect; there is no way to keep simulating in a hidden tab.
   */
  function pump(peer: Peer) {
    setStatus('connected')
    const history: number[] = []
    let frame = 0

    const send = setInterval(() => {
      history[frame] = frame % 16 // synthetic, but a changing bitfield
      const start = Math.max(0, frame - REDUNDANCY + 1)
      peer.send(encodeInputs(start, history.slice(start, frame + 1)))
      live.current = {...live.current, sent: live.current.sent + 1}
      frame++
    }, 1000 / 60)

    const show = setInterval(() => setStats(live.current), 250)

    const stop = () => {
      clearInterval(send)
      clearInterval(show)
    }
    // The panel closes the connection on unmount; stop the timers with it.
    const close = conn.current!.close
    conn.current!.close = () => {
      stop()
      close()
    }
  }

  if (role === 'idle') {
    return (
      <section className="net">
        <button onClick={() => void connect('host')}>host</button>
        <button onClick={() => setRole('guest')}>join</button>
        <p className="keys">{status}</p>
      </section>
    )
  }

  const connected = status === 'connected'
  return (
    <section className="net">
      <p className="keys">
        {role} · {status}
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
        <p className="keys">
          sent {stats.sent} · recv {stats.recv} · remote frame {stats.lastFrame} ·{' '}
          {stats.bytes} B/packet · recovered by redundancy {stats.recovered} · malformed{' '}
          {stats.malformed}
        </p>
      )}
    </section>
  )
}

const zero = {sent: 0, recv: 0, lastFrame: -1, bytes: 0, recovered: 0, malformed: 0}
