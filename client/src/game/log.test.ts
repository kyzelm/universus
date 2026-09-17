import {expect, test} from 'vitest'
import {encodeLog, LOG_HEADER} from './log'

/**
 * The byte layout, pinned against the table in tools/replay/main.go. The reader
 * is a different language in a different process, so nothing catches a writer
 * that drifts from it except a test on each side asserting the same offsets.
 */
test('the header carries the setup the match was started from', () => {
  const v = new DataView(
    encodeLog({training: true, ai: 2, chars: [1, 3]}, 0xdeadbeef, [0x0201, 0x0010]),
  )

  expect(String.fromCharCode(v.getUint8(0), v.getUint8(1), v.getUint8(2), v.getUint8(3))).toBe(
    'UNIV',
  )
  expect(v.getUint8(4)).toBe(1) // format version
  expect(v.getUint8(5)).toBe(1) // training
  expect(v.getUint8(6)).toBe(2) // AI tier
  expect(v.getUint8(7)).toBe(1) // player 1's character
  expect(v.getUint8(8)).toBe(3) // player 2's character
  expect([v.getUint8(9), v.getUint8(10), v.getUint8(11)]).toEqual([0, 0, 0]) // reserved
  expect(v.getUint32(12, true)).toBe(0xdeadbeef)

  // The inputs start where the header ends, little-endian whatever the machine.
  expect(v.byteLength).toBe(LOG_HEADER + 4)
  expect(v.getUint16(LOG_HEADER, true)).toBe(0x0201)
  expect(v.getUint16(LOG_HEADER + 2, true)).toBe(0x0010)
  expect(v.getUint8(LOG_HEADER)).toBe(0x01)
})

test('a normal match writes a zeroed setup, not an absent one', () => {
  const v = new DataView(encodeLog({training: false, ai: 0, chars: [0, 0]}, 7, []))
  expect(v.byteLength).toBe(LOG_HEADER)
  expect(v.getUint8(5)).toBe(0)
  expect(v.getUint8(6)).toBe(0)
})
