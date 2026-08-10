import {createSamples, type Samples} from '../game/stats'
import {
  decodeInputs,
  decodePing,
  encodeInputs,
  encodePing,
  packetType,
  PacketType,
  REDUNDANCY,
  stampNow,
} from './packet'

/**
 * Rollback netcode driver. It owns the input history for both players, decides
 * what the remote player is about to do, and corrects the sim when it turns out
 * to be wrong.
 *
 * GGPO parameters: 2 frames of input delay, 8 frames of maximum rollback,
 * prediction is "they are still holding what they last held".
 */
export const INPUT_DELAY = 2
export const MAX_ROLLBACK = 8

/** The sim, from the driver's side. Small on purpose: it is stubbed in tests. */
export interface SimBridge {
  advance(p1: number, p2: number): void
  rewind(frame: number): boolean
}

export interface NetplayStats {
  /** Frames simulated, not counting replays. */
  frames: number
  rollbacks: number
  /** Count by depth, index 1..MAX_ROLLBACK. */
  depths: number[]
  /** Frames advanced on a guess, and how many of those guesses were wrong. */
  predicted: number
  mispredicted: number
  /** Frames we refused to simulate because the remote was too far behind. */
  stalls: number
  /** Corrections that arrived too late to apply — a desync, if it ever fires. */
  dropped: number
  malformed: number
  rtt: Samples
  replayMs: Samples
}

export interface Netplay {
  /** Simulates one frame. False means it stalled and no frame was simulated. */
  step(localBits: number): boolean
  receive(data: ArrayBuffer): void
  ping(): void
  readonly frame: number
  readonly stats: NetplayStats
}

/**
 * @param seat which player this end drives — 0 is P1, 1 is P2. The sim always
 * takes (p1, p2) in that order on both machines, because the update order is
 * part of the spec and swapping it per-machine is a desync.
 */
export function createNetplay(
  sim: SimBridge,
  send: (data: ArrayBuffer) => void,
  seat: 0 | 1,
): Netplay {
  const local: number[] = []
  const remote: number[] = []
  const known: boolean[] = []

  /** What we actually fed the sim for the remote player at each frame. */
  const used: number[] = []

  let frame = 0
  /** Highest frame with remote input known contiguously from the start. */
  let confirmed = -1

  const stats: NetplayStats = {
    frames: 0,
    rollbacks: 0,
    depths: Array(MAX_ROLLBACK + 1).fill(0),
    predicted: 0,
    mispredicted: 0,
    stalls: 0,
    dropped: 0,
    malformed: 0,
    rtt: createSamples(),
    replayMs: createSamples(),
  }

  /**
   * Prediction: repeat the last input we actually saw. Scanning back rather
   * than caching one value keeps replays honest — during a replay the newest
   * known input can be *older* than the frame being replayed.
   */
  function remoteAt(f: number): number {
    if (known[f]) return remote[f]
    for (let k = f - 1; k >= 0 && f - k <= REDUNDANCY * 4; k--) {
      if (known[k]) return remote[k]
    }
    return 0
  }

  function simulate(f: number, remoteBits: number): void {
    used[f] = remoteBits
    const mine = local[f] ?? 0
    if (seat === 0) sim.advance(mine, remoteBits)
    else sim.advance(remoteBits, mine)
  }

  function sendInputs(through: number): void {
    const start = Math.max(0, through - REDUNDANCY + 1)
    const window = new Uint16Array(through - start + 1)
    for (let f = start; f <= through; f++) window[f - start] = local[f] ?? 0
    send(encodeInputs(start, window))
  }

  /** Rewind to `to` and re-simulate everything since with what we now know. */
  function rollback(to: number): void {
    const depth = frame - to
    if (!sim.rewind(to)) {
      // Older than the rollback window. Nothing can fix this frame; the two
      // sims have already diverged and M2 has to resynchronise.
      stats.dropped++
      return
    }

    const t0 = performance.now()
    const end = frame
    for (let f = to; f < end; f++) simulate(f, remoteAt(f))
    stats.replayMs.push(performance.now() - t0)

    stats.rollbacks++
    stats.depths[Math.min(depth, MAX_ROLLBACK)]++
  }

  return {
    get frame() {
      return frame
    },
    get stats() {
      return stats
    },

    step(localBits) {
      // Our own input lands INPUT_DELAY frames from now, so it has time to
      // reach the other end before the frame it belongs to is simulated.
      const at = frame + INPUT_DELAY
      local[at] = localBits
      sendInputs(at)

      // Never outrun the window: a misprediction older than MAX_ROLLBACK
      // cannot be corrected, so wait rather than desync. This is the stall
      // that shows up as a hitch when the connection is bad, and its
      // frequency is one of the numbers worth reporting.
      if (frame - confirmed > MAX_ROLLBACK) {
        stats.stalls++
        return false
      }

      if (!known[frame]) stats.predicted++
      simulate(frame, remoteAt(frame))
      frame++
      stats.frames++
      return true
    },

    receive(data) {
      let earliest = -1
      try {
        if (packetType(data) === PacketType.ping) {
          const {stamp, reply} = decodePing(data)
          if (reply) stats.rtt.push((stampNow() - stamp) / 10)
          else send(encodePing(stamp, true))
          return
        }

        const {startFrame, inputs} = decodeInputs(data)
        for (let i = 0; i < inputs.length; i++) {
          const f = startFrame + i
          if (known[f]) continue // redundancy: already had it, or it was resent
          known[f] = true
          remote[f] = inputs[i]

          // Already simulated this frame on a guess, and the guess was wrong.
          if (f < frame && used[f] !== inputs[i]) {
            stats.mispredicted++
            if (earliest < 0 || f < earliest) earliest = f
          }
        }
      } catch {
        stats.malformed++
        return
      }

      while (known[confirmed + 1]) confirmed++
      if (earliest >= 0) rollback(earliest)
    },

    ping() {
      send(encodePing(stampNow(), false))
    },
  }
}
