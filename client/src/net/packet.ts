/**
 * Net packet format. Little-endian, ~22 bytes for a full inputs packet:
 *
 *   [uint8 type][uint32 startFrame][uint8 count][uint16 inputs[count]]
 *
 * The uint16s are the same bytes the sim, the replay and the server see. No
 * translation layer anywhere on that path — a translation layer is a place for
 * the two ends to disagree.
 */
export const PacketType = {inputs: 1, checksum: 2, ping: 3, control: 4} as const

/**
 * Frames of history in every inputs packet. This redundancy replaces
 * retransmission entirely: the channel is unreliable and unordered, and a lost
 * packet is covered by each of the next seven. Retransmitting a 16 ms-old input
 * would deliver it long after the frame it belonged to.
 */
export const REDUNDANCY = 8

const HEADER = 6

/** The type byte, without decoding the rest. Cheap enough to call on dispatch. */
export function packetType(data: ArrayBuffer): number {
  if (data.byteLength < 1) throw new Error('empty packet')
  return new DataView(data).getUint8(0)
}

export interface InputsPacket {
  startFrame: number
  inputs: Uint16Array
}

export function encodeInputs(startFrame: number, inputs: ArrayLike<number>): ArrayBuffer {
  if (inputs.length === 0 || inputs.length > 255) {
    throw new RangeError(`count ${inputs.length} does not fit a uint8`)
  }
  if (!Number.isInteger(startFrame) || startFrame < 0 || startFrame > 0xffffffff) {
    throw new RangeError(`startFrame ${startFrame} does not fit a uint32`)
  }

  const buf = new ArrayBuffer(HEADER + inputs.length * 2)
  const v = new DataView(buf)
  v.setUint8(0, PacketType.inputs)
  v.setUint32(1, startFrame, true)
  v.setUint8(5, inputs.length)
  for (let i = 0; i < inputs.length; i++) v.setUint16(HEADER + i * 2, inputs[i], true)
  return buf
}

/**
 * Everything here came off the wire, so every field is checked before it is
 * used. A malformed packet throws and the caller drops it; on an unreliable
 * channel a dropped packet is already the normal case.
 */
export function decodeInputs(data: ArrayBuffer): InputsPacket {
  if (data.byteLength < HEADER) {
    throw new Error(`packet is ${data.byteLength} bytes, shorter than the ${HEADER}-byte header`)
  }

  const v = new DataView(data)
  const type = v.getUint8(0)
  if (type !== PacketType.inputs) throw new Error(`expected an inputs packet, got type ${type}`)

  const count = v.getUint8(5)
  if (data.byteLength !== HEADER + count * 2) {
    throw new Error(`count ${count} does not match a ${data.byteLength}-byte packet`)
  }

  // ponytail: a Uint16Array view over the buffer would be zero-copy and the
  // offset happens to be aligned, but typed arrays read platform endianness.
  // Eight DataView reads cost nothing and are little-endian by construction.
  const inputs = new Uint16Array(count)
  for (let i = 0; i < count; i++) inputs[i] = v.getUint16(HEADER + i * 2, true)

  return {startFrame: v.getUint32(1, true), inputs}
}

/**
 * Ping: [uint8 type][uint32 stamp][uint8 reply], 6 bytes. The stamp is the
 * sender's own clock and comes back untouched, so RTT is one subtraction and
 * there is no table of outstanding pings to keep.
 *
 * Tenths of a millisecond, not milliseconds: on a LAN the whole round trip can
 * be under 1 ms, and a measurement that reports 0 is not a measurement.
 */
export function encodePing(stamp: number, reply: boolean): ArrayBuffer {
  const buf = new ArrayBuffer(6)
  const v = new DataView(buf)
  v.setUint8(0, PacketType.ping)
  v.setUint32(1, stamp >>> 0, true)
  v.setUint8(5, reply ? 1 : 0)
  return buf
}

export function decodePing(data: ArrayBuffer): {stamp: number; reply: boolean} {
  if (data.byteLength !== 6) throw new Error(`ping is ${data.byteLength} bytes, want 6`)
  const v = new DataView(data)
  if (v.getUint8(0) !== PacketType.ping) throw new Error(`not a ping: type ${v.getUint8(0)}`)
  return {stamp: v.getUint32(1, true), reply: v.getUint8(5) !== 0}
}

/** The clock both ends stamp pings with: tenths of a ms, wrapped to uint32. */
export function stampNow(): number {
  return Math.round(performance.now() * 10) >>> 0
}
