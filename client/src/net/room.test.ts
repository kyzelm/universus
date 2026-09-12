import {afterEach, beforeEach, describe, expect, it, vi} from 'vitest'
import {FALLBACK_MS, negotiate, type Connectors, type Signal} from './room'
import type {Connection, Peer} from './peer'

/** Drains pending microtasks and any timer due now. */
const flush = () => vi.advanceTimersByTimeAsync(0)

function fakeSignal() {
  const sent: string[] = []
  const relayed: ArrayBuffer[] = []
  const state = {closed: false}
  let text: (t: string) => void = () => {}
  let binary: (d: ArrayBuffer) => void = () => {}

  const relayPeer: Peer = {send: (d) => relayed.push(d), close: () => {}, open: true}

  const signal: Signal = {
    send: (t) => sent.push(t),
    onText: (cb) => (text = cb),
    onBinary: (cb) => (binary = cb),
    asPeer: () => relayPeer,
    close: () => (state.closed = true),
  }

  return {
    signal,
    sent,
    relayed,
    state,
    arrive: (t: string) => text(t),
    arriveBinary: (d: ArrayBuffer) => binary(d),
  }
}

function fakeConnectors() {
  const accepted: string[] = []
  const state = {closed: false, offers: 0, answers: 0}
  const channel: Peer = {send: () => {}, close: () => {}, open: true}

  let connect!: (p: Peer) => void
  const conn: Connection = {
    localBlob: 'local-blob',
    ready: new Promise<Peer>((resolve) => (connect = resolve)),
    accept: async (blob) => void accepted.push(blob),
    close: () => (state.closed = true),
  }

  const connectors: Connectors = {
    host: async () => {
      state.offers++
      return conn
    },
    guest: async (offer) => {
      state.answers++
      accepted.push(offer)
      return conn
    },
  }

  return {connectors, accepted, state, channel, connect: () => connect(channel)}
}

describe('room negotiation', () => {
  beforeEach(() => vi.useFakeTimers())
  afterEach(() => vi.useRealTimers())

  it('hosts: offers, takes the answer, and plays P2P when the channel opens', async () => {
    const sig = fakeSignal()
    const rtc = fakeConnectors()

    const session = negotiate(sig.signal, () => {}, rtc.connectors)
    sig.arrive('host')
    await flush()

    expect(rtc.state.offers).toBe(1)
    expect(sig.sent).toEqual(['local-blob'])

    sig.arrive('their-answer')
    await flush()
    expect(rtc.accepted).toEqual(['their-answer'])

    rtc.connect()
    await flush()

    const s = await session
    expect(s.kind).toBe('p2p')
    expect(s.seat).toBe(0)
    expect(s.peer).toBe(rtc.channel)
    // The verdict goes out, then the room is no longer needed.
    expect(sig.sent).toEqual(['local-blob', 'p2p'])
    expect(sig.state.closed).toBe(true)
  })

  // D15. The channel that never opens is the whole reason the relay exists.
  it('hosts: falls back to the relay after five seconds and says so', async () => {
    const sig = fakeSignal()
    const rtc = fakeConnectors()
    const got: ArrayBuffer[] = []

    const session = negotiate(sig.signal, (d) => got.push(d), rtc.connectors)
    sig.arrive('host')
    await flush()
    sig.arrive('their-answer')

    await vi.advanceTimersByTimeAsync(FALLBACK_MS - 1)
    expect(sig.sent).toEqual(['local-blob'])

    await vi.advanceTimersByTimeAsync(1)
    const s = await session

    expect(s.kind).toBe('relay')
    expect(sig.sent).toEqual(['local-blob', 'relay'])
    // The socket is the transport now, so it stays open and the peer connection
    // does not.
    expect(sig.state.closed).toBe(false)
    expect(rtc.state.closed).toBe(true)

    // Binary frames on the room are the match from here on.
    const packet = new Uint8Array([1, 2, 3]).buffer
    sig.arriveBinary(packet)
    expect(got).toEqual([packet])
  })

  it('joins: answers the offer and takes seat 1', async () => {
    const sig = fakeSignal()
    const rtc = fakeConnectors()

    const session = negotiate(sig.signal, () => {}, rtc.connectors)
    sig.arrive('guest')
    await flush()

    // Nothing is sent before the offer arrives — there is nothing to answer yet.
    expect(sig.sent).toEqual([])

    sig.arrive('their-offer')
    await flush()
    expect(rtc.accepted).toEqual(['their-offer'])
    expect(sig.sent).toEqual(['local-blob'])

    rtc.connect()
    sig.arrive('p2p')
    await flush()

    const s = await session
    expect(s.kind).toBe('p2p')
    expect(s.seat).toBe(1)
  })

  /**
   * The host decides for both, and a guest whose own channel did open still
   * plays over the relay. One end on P2P and the other on the relay is a match
   * where neither sees the other's inputs.
   */
  it('joins: follows the relay verdict even with a working channel', async () => {
    const sig = fakeSignal()
    const rtc = fakeConnectors()

    const session = negotiate(sig.signal, () => {}, rtc.connectors)
    sig.arrive('guest')
    await flush()
    sig.arrive('their-offer')
    await flush()

    rtc.connect()
    sig.arrive('relay')
    await flush()

    expect((await session).kind).toBe('relay')
  })

  it('joins: refuses a P2P verdict it cannot honour', async () => {
    const sig = fakeSignal()
    const rtc = fakeConnectors()

    const session = negotiate(sig.signal, () => {}, rtc.connectors)
    sig.arrive('guest')
    await flush()
    sig.arrive('their-offer')
    await flush()

    sig.arrive('p2p') // and the channel never opens here
    const failed = expect(session).rejects.toThrow(/never connected/)
    await vi.advanceTimersByTimeAsync(FALLBACK_MS)
    await failed
  })

  it('fails on a room that does not speak the protocol', async () => {
    const sig = fakeSignal()
    const session = negotiate(sig.signal, () => {}, fakeConnectors().connectors)

    sig.arrive('room full')
    await expect(session).rejects.toThrow(/where a role belongs/)
  })
})
