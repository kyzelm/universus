import {describe, expect, test} from 'vitest'
import {decodeInputs, encodeInputs, PacketType, REDUNDANCY} from './packet'

test('a full redundancy window is 22 bytes', () => {
  expect(encodeInputs(0, new Uint16Array(REDUNDANCY)).byteLength).toBe(22)
})

// The layout is a wire format: two machines agree on these exact bytes or they
// desync. Pin them, so a "harmless" refactor cannot quietly move a field.
test('the byte layout is little-endian and packed', () => {
  const bytes = new Uint8Array(encodeInputs(0x01020304, [0x0a0b, 0x0c0d]))
  expect([...bytes]).toEqual([
    PacketType.inputs,
    0x04, 0x03, 0x02, 0x01, // startFrame, little-endian
    2, // count
    0x0b, 0x0a, // inputs[0]
    0x0d, 0x0c, // inputs[1]
  ])
})

test('round trips', () => {
  const inputs = [0, 1, 0x0f, 0x077f, 0xffff, 4, 5, 6]
  const got = decodeInputs(encodeInputs(4_000_000_000, inputs))
  expect(got.startFrame).toBe(4_000_000_000)
  expect([...got.inputs]).toEqual(inputs)
})

describe('rejects what came off the wire malformed', () => {
  test('a packet shorter than the header', () => {
    expect(() => decodeInputs(new ArrayBuffer(5))).toThrow(/shorter than/)
  })

  test('a type that is not inputs', () => {
    const buf = encodeInputs(1, [7])
    new DataView(buf).setUint8(0, PacketType.ping)
    expect(() => decodeInputs(buf)).toThrow(/type 3/)
  })

  test('a count that disagrees with the length', () => {
    const buf = encodeInputs(1, [7, 8])
    new DataView(buf).setUint8(5, 9)
    expect(() => decodeInputs(buf)).toThrow(/count 9/)
  })

  test('a truncated tail', () => {
    const buf = encodeInputs(1, [7, 8]).slice(0, 8)
    expect(() => decodeInputs(buf)).toThrow(/count 2/)
  })
})

describe('rejects what cannot be encoded', () => {
  test('an empty or oversized window', () => {
    expect(() => encodeInputs(0, [])).toThrow(/uint8/)
    expect(() => encodeInputs(0, new Uint16Array(256))).toThrow(/uint8/)
  })

  test('a frame number outside uint32', () => {
    expect(() => encodeInputs(-1, [0])).toThrow(/uint32/)
    expect(() => encodeInputs(2 ** 32, [0])).toThrow(/uint32/)
    expect(() => encodeInputs(1.5, [0])).toThrow(/uint32/)
  })
})
