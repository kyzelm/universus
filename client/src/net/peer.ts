/**
 * WebRTC DataChannel with manual signalling: the offer and answer blobs are
 * carried between the two ends by hand (copy, paste). M0 only has to prove the
 * transport works; the signalling server is M3's job.
 *
 * Flow:
 *   host                              guest
 *   host() -> localBlob  ─────────────> guest(blob) -> localBlob
 *   accept(blob)         <─────────────┘
 *   ready                              ready
 */

const CHANNEL = 'inputs'

/**
 * Unreliable and unordered on purpose. A retransmitted input arrives after the
 * frame it belonged to has already been simulated, and every packet already
 * carries the last 8 frames, so there is nothing worth retransmitting.
 */
const CHANNEL_INIT: RTCDataChannelInit = {ordered: false, maxRetransmits: 0}

/**
 * STUN only, no TURN — a relay costs bandwidth and adds a hop, and the fallback
 * for peers that cannot hole-punch is the WebSocket relay we run ourselves.
 *
 * This does reach a Google server to learn our public address, which is how
 * every WebRTC app discovers it. Swap it for our own STUN in M3.
 */
const ICE: RTCConfiguration = {iceServers: [{urls: 'stun:stun.l.google.com:19302'}]}

export interface Peer {
  send(data: ArrayBuffer): void
  close(): void
  readonly open: boolean
}

export interface Connection {
  /** Paste this into the other end. */
  readonly localBlob: string
  /** Host only: feed back the blob the guest produced. */
  accept?(remoteBlob: string): Promise<void>
  /** Resolves when the channel is open and can carry packets. */
  readonly ready: Promise<Peer>
  close(): void
}

export async function host(onMessage: (data: ArrayBuffer) => void): Promise<Connection> {
  const pc = new RTCPeerConnection(ICE)
  const ready = wire(pc.createDataChannel(CHANNEL, CHANNEL_INIT), onMessage)

  await pc.setLocalDescription(await pc.createOffer())
  await gathered(pc)

  return {
    localBlob: encodeBlob(pc.localDescription!),
    accept: async (blob) => pc.setRemoteDescription(decodeBlob(blob, 'answer')),
    ready,
    close: () => pc.close(),
  }
}

export async function guest(
  offerBlob: string,
  onMessage: (data: ArrayBuffer) => void,
): Promise<Connection> {
  const pc = new RTCPeerConnection(ICE)

  // The guest does not create the channel, it receives the host's.
  const ready = new Promise<Peer>((resolve, reject) => {
    pc.ondatachannel = (e) => wire(e.channel, onMessage).then(resolve, reject)
  })

  await pc.setRemoteDescription(decodeBlob(offerBlob, 'offer'))
  await pc.setLocalDescription(await pc.createAnswer())
  await gathered(pc)

  return {localBlob: encodeBlob(pc.localDescription!), ready, close: () => pc.close()}
}

function wire(ch: RTCDataChannel, onMessage: (data: ArrayBuffer) => void): Promise<Peer> {
  ch.binaryType = 'arraybuffer'
  ch.onmessage = (e) => onMessage(e.data as ArrayBuffer)

  const peer: Peer = {
    // Sending on a channel that is not open throws. Dropping instead is
    // correct here: this channel drops packets by design and the next one
    // carries the same inputs again.
    send: (data) => {
      if (ch.readyState === 'open') ch.send(data)
    },
    close: () => ch.close(),
    get open() {
      return ch.readyState === 'open'
    },
  }

  if (ch.readyState === 'open') return Promise.resolve(peer)
  return new Promise((resolve, reject) => {
    ch.onopen = () => resolve(peer)
    ch.onerror = () => reject(new Error('data channel failed to open'))
  })
}

/**
 * Waits for ICE gathering to finish so the blob carries every candidate and
 * there is nothing left to trickle — which is what makes copy-paste signalling
 * possible at all.
 *
 * Capped: where STUN is unreachable, gathering never completes, and the host
 * candidates already in hand are enough for two tabs or a LAN.
 */
function gathered(pc: RTCPeerConnection, capMs = 3000): Promise<void> {
  if (pc.iceGatheringState === 'complete') return Promise.resolve()

  return new Promise((resolve) => {
    const finish = () => {
      clearTimeout(timer)
      pc.removeEventListener('icegatheringstatechange', check)
      resolve()
    }
    const check = () => {
      if (pc.iceGatheringState === 'complete') finish()
    }
    const timer = setTimeout(finish, capMs)
    pc.addEventListener('icegatheringstatechange', check)
  })
}

/** base64 so the blob survives a copy-paste through anything, on one line. */
function encodeBlob(desc: RTCSessionDescription): string {
  return btoa(JSON.stringify({type: desc.type, sdp: desc.sdp}))
}

function decodeBlob(blob: string, want: 'offer' | 'answer'): RTCSessionDescriptionInit {
  let parsed: unknown
  try {
    parsed = JSON.parse(atob(blob.trim()))
  } catch {
    throw new Error('that does not look like a signalling blob')
  }

  const {type, sdp} = (parsed ?? {}) as RTCSessionDescriptionInit
  if (type !== want) throw new Error(`expected ${want}, got ${type}`)
  if (typeof sdp !== 'string' || sdp === '') throw new Error(`${want} has no sdp`)
  return {type, sdp}
}
