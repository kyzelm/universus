import {guest, host, type Connection, type Peer} from './peer'

/**
 * Signalling and the relay fallback over one socket to our own server
 * (02 Architecture/Transport and Connectivity.md).
 *
 *   ws://host:8080/ws?room=CODE
 *
 * Both ends ask for the same code; the server pairs them, tells each which it
 * is, and from then on copies whatever one sends to the other. The pair trade
 * SDP over it, try WebRTC, and — if ICE cannot get through, which is 15-20% of
 * networks and plausibly the one the defense happens on — keep the same socket
 * as the match transport (Decision Log D15).
 *
 * The relay is a TURN substitute we already pay for: it carries ~9.6 kbit/s per
 * direction, which is nothing, and it speaks the game's own packets.
 */

/** D15: WebRTC gets five seconds, then the match starts over the relay. */
export const FALLBACK_MS = 5000

export type Kind = 'p2p' | 'relay'
export type Role = 'host' | 'guest'

export interface Session {
  readonly peer: Peer
  /** Which transport the match is actually on — a measured figure, not a guess. */
  readonly kind: Kind
  /** The host drives P1. */
  readonly seat: 0 | 1
  close(): void
}

/** What a room looks like to the negotiation: text now, bytes possibly later. */
export interface Signal {
  send(text: string): void
  onText(cb: (text: string) => void): void
  onBinary(cb: (data: ArrayBuffer) => void): void
  /** The same socket as the match transport, once P2P has been given up on. */
  asPeer(): Peer
  close(): void
}

/** Injected so the negotiation can be tested without a WebRTC stack. */
export interface Connectors {
  host(onMessage: (data: ArrayBuffer) => void): Promise<Connection>
  guest(offer: string, onMessage: (data: ArrayBuffer) => void): Promise<Connection>
}

export async function joinRoom(
  code: string,
  onMessage: (data: ArrayBuffer) => void,
): Promise<Session> {
  return negotiate(await openRoom(relayURL(code)), onMessage)
}

/**
 * The signalling server, defaulting to one on the machine serving the page —
 * which is what makes a two-machine test work: the guest opens the host's IP,
 * so the relay is found at the host's IP too. `?relay=ws://…` overrides it, at
 * runtime rather than at build time, because the deployed address is not known
 * when the bundle is built.
 */
export function relayURL(code: string): string {
  const base = new URLSearchParams(location.search).get('relay') ?? `ws://${location.hostname}:8080`
  return `${base}/ws?room=${encodeURIComponent(code)}`
}

export async function negotiate(
  sig: Signal,
  onMessage: (data: ArrayBuffer) => void,
  connectors: Connectors = {host, guest},
  fallbackMs = FALLBACK_MS,
): Promise<Session> {
  const text = queue(sig)

  // The first frame the server ever sends is the role, and it is sent when the
  // pair is complete — so "you are the host" also means "the other end is here,
  // start offering".
  const role = await text.next()
  if (role !== 'host' && role !== 'guest') {
    throw new Error(`the room sent ${JSON.stringify(role)} where a role belongs`)
  }

  const conn =
    role === 'host'
      ? await connectors.host(onMessage)
      : await connectors.guest(await text.next(), onMessage)
  sig.send(conn.localBlob)
  if (role === 'host') await conn.accept!(await text.next())

  const kind =
    role === 'host' ? await decide(conn, sig, fallbackMs) : await follow(conn, text, fallbackMs)
  const seat = role === 'host' ? 0 : 1

  if (kind === 'p2p') {
    const peer = await conn.ready
    sig.close() // the room has done its job
    return {peer, kind, seat, close: () => conn.close()}
  }

  conn.close()
  sig.onBinary(onMessage)
  return {peer: sig.asPeer(), kind, seat, close: () => sig.close()}
}

/**
 * The host alone decides, and says so. Both ends timing their own ICE is the
 * one failure that would read as a desync: a channel that opens at 4.9 s on one
 * machine and 5.1 s on the other leaves one end on P2P and the other on the
 * relay, each talking to nobody.
 */
async function decide(conn: Connection, sig: Signal, fallbackMs: number): Promise<Kind> {
  const ready = await within(conn.ready, fallbackMs)
  const kind: Kind = ready === TIMEOUT ? 'relay' : 'p2p'
  sig.send(kind)
  return kind
}

async function follow(
  conn: Connection,
  text: {next(): Promise<string>},
  fallbackMs: number,
): Promise<Kind> {
  // Twice the host's budget: its verdict is sent at the five-second mark at the
  // latest, and has a trip to make after that.
  const verdict = await within(text.next(), fallbackMs * 2)
  if (verdict === TIMEOUT) throw new Error('the host never said which transport to use')
  if (verdict !== 'p2p' && verdict !== 'relay') {
    throw new Error(`the host sent ${JSON.stringify(verdict)} where a transport belongs`)
  }

  // Same ICE negotiation, so a channel the host has open is about to be open
  // here. If it is not, the verdict is wrong and playing on would be worse.
  if (verdict === 'p2p' && (await within(conn.ready, fallbackMs)) === TIMEOUT) {
    throw new Error('the host is on P2P and this end never connected')
  }
  return verdict
}

/**
 * Signalling text arrives whether or not anybody is awaiting it — the offer can
 * land while we are still generating our own description — so it is queued
 * rather than dropped.
 */
function queue(sig: Signal) {
  const waiting: string[] = []
  let want: ((text: string) => void) | null = null

  sig.onText((text) => {
    if (!want) return void waiting.push(text)
    const resolve = want
    want = null
    resolve(text)
  })

  return {
    next(): Promise<string> {
      const text = waiting.shift()
      if (text !== undefined) return Promise.resolve(text)
      return new Promise((resolve) => (want = resolve))
    },
  }
}

const TIMEOUT = Symbol('timeout')

function within<T>(p: Promise<T>, ms: number): Promise<T | typeof TIMEOUT> {
  let timer: ReturnType<typeof setTimeout>
  const timeout = new Promise<typeof TIMEOUT>((resolve) => {
    timer = setTimeout(() => resolve(TIMEOUT), ms)
  })
  return Promise.race([p, timeout]).finally(() => clearTimeout(timer))
}

export async function openRoom(url: string): Promise<Signal> {
  const ws = new WebSocket(url)
  ws.binaryType = 'arraybuffer'

  await new Promise<void>((resolve, reject) => {
    ws.onopen = () => resolve()
    ws.onerror = () => reject(new Error(`no relay at ${url}`))
    ws.onclose = (e) => reject(new Error(e.reason || `the relay refused the room (${e.code})`))
  })
  ws.onclose = null

  let text: ((t: string) => void) | null = null
  let binary: ((d: ArrayBuffer) => void) | null = null

  // Text is signalling, binary is the match. The server never looks at either,
  // so the message type is the whole discriminator.
  ws.onmessage = (e) =>
    typeof e.data === 'string' ? text?.(e.data) : binary?.(e.data as ArrayBuffer)

  return {
    send: (t) => ws.send(t),
    onText: (cb) => (text = cb),
    onBinary: (cb) => (binary = cb),
    asPeer: () => ({
      // Same rule as the data channel: a packet sent on a socket that is not
      // open is dropped, and the next one carries the same inputs again.
      send: (data) => {
        if (ws.readyState === WebSocket.OPEN) ws.send(data)
      },
      close: () => ws.close(),
      get open() {
        return ws.readyState === WebSocket.OPEN
      },
    }),
    close: () => ws.close(),
  }
}
