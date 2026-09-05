import {createSamples, type Samples} from '../game/stats'

/**
 * The artificial network condition layer — added latency, jitter and packet
 * loss, applied in software (02 Architecture/Transport and Connectivity.md).
 *
 * **Localhost lies.** Two tabs on one machine exchange packets in under a
 * millisecond, which means every rollback path in the netcode is exercised at a
 * depth of one and the interesting half of the implementation never runs. This
 * layer is what turns "it works on my machine" into the measurement matrix the
 * results chapter is made of: 0/50/100/150/200 ms against 0/2/5% loss
 * (05 Tooling/Testing and CI.md).
 *
 * It sits on the **inbound** side, between the data channel and the netplay
 * driver. Inbound because that is the half one end can impair without the other
 * end's cooperation, and because dropping a packet on arrival is
 * indistinguishable from the network having dropped it. The send path stays
 * exactly as it is, which matters: it runs inside the frame loop.
 *
 * The sim never learns any of this exists. Latency is a transport property, and
 * a simulation that could see it would be a simulation whose output depended on
 * the network — which is the one thing rollback exists to prevent.
 */
export interface Impairment {
  /** Delay added to every arriving packet, in milliseconds, one way. */
  delayMs: number
  /**
   * Uniform spread either side of that delay. Packets are scheduled
   * independently, so jitter reorders them — which is honest: the channel is
   * unordered by design and the netcode has to cope with it.
   */
  jitterMs: number
  /** Chance a packet is dropped outright, 0-100. */
  lossPercent: number
}

/** No impairment: the packet goes straight through, with no timer in the way. */
export const CLEAN: Impairment = {delayMs: 0, jitterMs: 0, lossPercent: 0}

export interface LinkStats {
  arrived: number
  dropped: number
  /** The delay actually applied, so the HUD reports what happened. */
  delay: Samples
}

export interface Link {
  /** Feed an arriving packet in. It reaches the driver late, or never. */
  receive(data: ArrayBuffer): void
  set(cfg: Impairment): void
  readonly cfg: Impairment
  readonly stats: LinkStats
  /** Drops every packet still in flight. Call it when the peer goes away. */
  dispose(): void
}

/**
 * @param deliver where a packet goes once the link is done with it.
 * @param seed the impairment PRNG's seed. Seeded on purpose: a measurement run
 * that cannot be repeated is an anecdote, and "5% loss" has to mean the same
 * 5% twice. It is deliberately **not** the sim's generator — nothing here may
 * ever touch simulation state.
 */
export function createLink(
  deliver: (data: ArrayBuffer) => void,
  cfg: Impairment = CLEAN,
  seed = 1,
): Link {
  const rand = createRng(seed)
  const stats: LinkStats = {arrived: 0, dropped: 0, delay: createSamples()}
  const pending = new Set<ReturnType<typeof setTimeout>>()
  let current = {...cfg}

  return {
    get cfg() {
      return current
    },
    get stats() {
      return stats
    },

    set(next) {
      current = {...next}
      // A new condition is a new measurement: counters from the previous cell
      // of the matrix would otherwise be read as belonging to this one.
      stats.arrived = 0
      stats.dropped = 0
      stats.delay = createSamples()
    },

    receive(data) {
      // The clean path is synchronous and allocates nothing: with the layer off
      // the link is not in the way at all, so a real match is measured through
      // the same code that plays it.
      if (current.delayMs <= 0 && current.jitterMs <= 0 && current.lossPercent <= 0) {
        stats.arrived++
        deliver(data)
        return
      }

      if (rand() * 100 < current.lossPercent) {
        stats.dropped++
        return
      }

      // Every packet already carries the last few frames of inputs, so a
      // dropped one costs nothing as long as its successor arrives — that
      // redundancy is what replaces retransmission, and this layer is how it
      // gets tested.
      const ms = Math.max(0, current.delayMs + (rand() * 2 - 1) * current.jitterMs)
      stats.delay.push(ms)

      const id = setTimeout(() => {
        pending.delete(id)
        stats.arrived++
        deliver(data)
      }, ms)
      pending.add(id)
    },

    dispose() {
      for (const id of pending) clearTimeout(id)
      pending.clear()
    },
  }
}

/**
 * xorshift32, the same generator the sim uses, for the same reason: small,
 * seedable and identical everywhere. Its output here is cosmetic — it decides
 * which packets suffer — but reproducibility is the whole point of seeding it.
 */
function createRng(seed: number): () => number {
  let x = seed >>> 0 || 1
  return () => {
    x ^= x << 13
    x >>>= 0
    x ^= x >>> 17
    x ^= x << 5
    x >>>= 0
    return x / 0x100000000
  }
}
