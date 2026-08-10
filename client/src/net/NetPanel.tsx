import {useEffect, useRef, useState} from 'react'
import type {Game} from '../game/game'
import {guest, host, type Connection} from './peer'

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
    </section>
  )
}
