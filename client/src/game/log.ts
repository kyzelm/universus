import type {Setup} from '../sim/wasm'

/**
 * The replay log format, written here and read by tools/replay (see its package
 * comment for the table this must match). A 16-byte header carrying the setup,
 * then little-endian uint16 pairs, player 1 then player 2, one pair per frame.
 *
 * The header exists because the inputs alone are not the match. The log records
 * what the *caller* fed the sim, and two modes generate inputs the caller never
 * sent — the AI's presses come from inside the sim, and the lab changes what a
 * frame does. Replayed without its setup, a lab session comes back as one where
 * seat 2 stands still, silently.
 */
export const LOG_HEADER = 16
const MAGIC = 'UNIV'
const VERSION = 1

/**
 * Packs a session into the log format. Explicit DataView writes rather than a
 * Uint16Array, which would use the platform's byte order and silently produce a
 * different file on a big-endian machine.
 */
export function encodeLog(
  setup: Setup,
  dataVersion: number,
  inputs: readonly number[],
): ArrayBuffer {
  const buf = new ArrayBuffer(LOG_HEADER + inputs.length * 2)
  const v = new DataView(buf)

  for (let i = 0; i < MAGIC.length; i++) v.setUint8(i, MAGIC.charCodeAt(i))
  v.setUint8(4, VERSION)
  v.setUint8(5, setup.training ? 1 : 0)
  v.setUint8(6, setup.ai)
  v.setUint8(7, setup.chars[0])
  v.setUint8(8, setup.chars[1])
  // Bytes 9-11 stay zero: reserved, and the reader checks the header's size.

  // Recorded, and compared on the way back in rather than enforced: a log taken
  // against older frame data still replays, it just replays a match that is no
  // longer the same one. The warning is the explanation for the divergence.
  v.setUint32(12, dataVersion, true)

  for (let i = 0; i < inputs.length; i++) v.setUint16(LOG_HEADER + i * 2, inputs[i], true)
  return buf
}
