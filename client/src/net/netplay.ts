import {createSamples, type Samples} from '../game/stats'
import {
  decodeChecksum,
  decodeInputs,
  decodePing,
  encodeChecksum,
  encodeInputs,
  encodePing,
  packetType,
  decodeControl,
  encodeControl,
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

/**
 * Frames between checksum exchanges. Only frames whose inputs are all known
 * are compared — a predicted state legitimately differs between the two ends,
 * and comparing one would report a desync on every healthy connection.
 */
export const CHECKSUM_EVERY = 30

/**
 * The 99th-percentile rollback depth from the histogram — the number worth
 * reporting, since the mean depth of a healthy connection is near zero and
 * hides the frames that actually cost something.
 */
export function depthP99(depths: readonly number[]): number {
  const total = depths.reduce((a, b) => a + b, 0)
  if (total === 0) return 0

  let seen = 0
  for (let d = depths.length - 1; d >= 0; d--) {
    seen += depths[d]
    if (seen >= total * 0.01) return d
  }
  return 0
}

/** The sim, from the driver's side. Small on purpose: it is stubbed in tests. */
export interface SimBridge {
  advance(p1: number, p2: number): void
  rewind(frame: number): boolean
  /** Hash of the current state. Called once per CHECKSUM_EVERY frames. */
  checksum(): number
  /** Hash of the character data. Exchanged once, before the first frame. */
  dataVersion(): number
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
  /**
   * Set when the peer's character data does not match ours. Every frame after
   * that would diverge, so the match is refused rather than played.
   */
  dataMismatch: boolean

  /** Confirmed frames where both ends hashed the same state. */
  verified: number
  /** …and where they did not. The first such frame is where to start looking. */
  desyncs: number
  desyncFrame: number
  rtt: Samples
  replayMs: Samples
}

export interface Netplay {
  /** Simulates one frame. False means it stalled and no frame was simulated. */
  step(localBits: number): boolean
  /**
   * Zeroes the counters without touching the match. **A new network condition
   * is a new measurement**: the matrix cell being run has to be timed on its
   * own frames, or every cell after the first reports a blend of itself and
   * everything before it (01 Thesis/Measurement Methodology.md).
   */
  resetStats(): void
  /** Announces our character data hash. Sent once, on connect. */
  hello(): void
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

  /** Our state hash at the start of each checkpoint frame. */
  const sums = new Map<number, number>()
  /** Theirs, held until the frame is settled on our side too. */
  const theirSums = new Map<number, number>()
  let sentThrough = 0

  let frame = 0
  /** Highest frame with remote input known contiguously from the start. */
  let confirmed = -1

  const stats: NetplayStats = {
    dataMismatch: false,
    frames: 0,
    rollbacks: 0,
    depths: Array(MAX_ROLLBACK + 1).fill(0),
    predicted: 0,
    mispredicted: 0,
    stalls: 0,
    dropped: 0,
    malformed: 0,
    verified: 0,
    desyncs: 0,
    desyncFrame: -1,
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

    // Recorded on the way past, because the hash of frame N is unreachable once
    // the sim has moved on. A replay overwrites it with the corrected value,
    // which is exactly what should be compared.
    const next = f + 1
    if (next % CHECKSUM_EVERY === 0) sums.set(next, sim.checksum() >>> 0)
  }

  /**
   * Sends and compares checksums for frames that can no longer change. The
   * state at frame f is settled once every input before f is known, so
   * `confirmed + 1` is the newest frame worth hashing.
   */
  function settle(): void {
    const settled = confirmed + 1

    for (let f = sentThrough + CHECKSUM_EVERY; f <= settled; f += CHECKSUM_EVERY) {
      const sum = sums.get(f)
      if (sum === undefined) break // not simulated yet; it will go out later
      send(encodeChecksum(f, sum))
      sentThrough = f
    }

    for (const [f, theirs] of theirSums) {
      if (f > settled) continue // still predicted here; judging it now is unfair

      const ours = sums.get(f)
      if (ours === undefined) {
        // Settled but not simulated here yet — inputs can arrive faster than we
        // step. Hold it, unless it is old enough that we never will.
        if (f < settled - CHECKSUM_EVERY * 4) theirSums.delete(f)
        continue
      }

      theirSums.delete(f)
      if (ours === theirs) {
        stats.verified++
      } else {
        stats.desyncs++
        if (stats.desyncFrame < 0) stats.desyncFrame = f
      }
    }
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

      // Nothing is worth simulating against a peer with different frame data:
      // every frame would diverge and the checksums would report it without
      // ever saying why.
      if (stats.dataMismatch) return false

      if (!known[frame]) stats.predicted++
      simulate(frame, remoteAt(frame))
      frame++
      stats.frames++
      settle()
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

        if (packetType(data) === PacketType.control) {
          const {dataVersion} = decodeControl(data)
          if (dataVersion !== sim.dataVersion()) stats.dataMismatch = true
          return
        }

        if (packetType(data) === PacketType.checksum) {
          const {frame: f, sum} = decodeChecksum(data)
          theirSums.set(f, sum)
          settle()
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
      settle()
    },

    resetStats() {
      Object.assign(stats, {
        frames: 0,
        rollbacks: 0,
        depths: Array(MAX_ROLLBACK + 1).fill(0),
        predicted: 0,
        mispredicted: 0,
        stalls: 0,
        dropped: 0,
        malformed: 0,
        verified: 0,
        desyncs: 0,
        desyncFrame: -1,
        rtt: createSamples(),
        replayMs: createSamples(),
      })
      // dataMismatch is deliberately not cleared: it is a fact about the peer,
      // not a measurement, and it does not stop being true.
    },

    ping() {
      send(encodePing(stampNow(), false))
    },

    hello() {
      send(encodeControl(sim.dataVersion()))
    },
  }
}
