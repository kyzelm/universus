import {afterEach, beforeEach, describe, expect, it, vi} from 'vitest'
import {CLEAN, createLink, type Impairment} from './impair'

const packet = (n: number) => new Uint16Array([n]).buffer
const first = (data: ArrayBuffer) => new Uint16Array(data)[0]

function collect() {
  const got: number[] = []
  return {got, deliver: (d: ArrayBuffer) => got.push(first(d))}
}

describe('artificial network conditions', () => {
  beforeEach(() => vi.useFakeTimers())
  afterEach(() => vi.useRealTimers())

  // With the layer off the link must not be in the way at all: no timer, no
  // reordering, nothing deferred to a later task. A real match is measured
  // through the same code that plays it.
  it('passes packets straight through when clean', () => {
    const {got, deliver} = collect()
    const link = createLink(deliver, CLEAN)

    link.receive(packet(1))
    link.receive(packet(2))

    expect(got).toEqual([1, 2])
    expect(link.stats.arrived).toBe(2)
  })

  it('holds a packet for the configured delay', () => {
    const {got, deliver} = collect()
    const link = createLink(deliver, {delayMs: 50, jitterMs: 0, lossPercent: 0})

    link.receive(packet(1))
    vi.advanceTimersByTime(49)
    expect(got).toEqual([])

    vi.advanceTimersByTime(1)
    expect(got).toEqual([1])
    expect(link.stats.delay.percentile(50)).toBe(50)
  })

  it('drops every packet at 100% loss and none at 0%', () => {
    const lossy = collect()
    const link = createLink(lossy.deliver, {delayMs: 10, jitterMs: 0, lossPercent: 100})
    for (let i = 0; i < 20; i++) link.receive(packet(i))
    vi.advanceTimersByTime(1000)

    expect(lossy.got).toEqual([])
    expect(link.stats.dropped).toBe(20)

    const clean = collect()
    const keeps = createLink(clean.deliver, {delayMs: 10, jitterMs: 0, lossPercent: 0})
    for (let i = 0; i < 20; i++) keeps.receive(packet(i))
    vi.advanceTimersByTime(1000)

    expect(clean.got).toHaveLength(20)
    expect(keeps.stats.dropped).toBe(0)
  })

  // A measurement run that cannot be repeated is an anecdote. Two links on the
  // same seed must suffer exactly the same packets, and a different seed must
  // actually change which ones.
  it('drops the same packets for the same seed', () => {
    const cfg: Impairment = {delayMs: 0, jitterMs: 0, lossPercent: 30}
    const run = (seed: number) => {
      const {got, deliver} = collect()
      const link = createLink(deliver, cfg, seed)
      for (let i = 0; i < 200; i++) link.receive(packet(i))
      vi.advanceTimersByTime(100)
      return got
    }

    expect(run(7)).toEqual(run(7))
    expect(run(7)).not.toEqual(run(8))

    // …and it is roughly the rate asked for, not a rate the generator invented.
    expect(run(7).length).toBeGreaterThan(200 * 0.6)
    expect(run(7).length).toBeLessThan(200 * 0.8)
  })

  // Jitter is what reorders packets, and reordering is the condition the
  // unordered channel exists to tolerate. The bound is what is testable: a
  // delay outside it is a bug, and one inside it is the point.
  it('keeps jittered delays inside the window and never below zero', () => {
    const {deliver} = collect()
    const link = createLink(deliver, {delayMs: 20, jitterMs: 30, lossPercent: 0})

    for (let i = 0; i < 300; i++) link.receive(packet(i))
    vi.advanceTimersByTime(1000)

    const {delay} = link.stats
    expect(delay.percentile(0)).toBeGreaterThanOrEqual(0)
    expect(delay.percentile(100)).toBeLessThanOrEqual(50)
    // The spread is real: a jitter that never fires would pin every sample at
    // the base delay and this test would pass on a broken layer.
    expect(delay.percentile(100) - delay.percentile(0)).toBeGreaterThan(10)
  })

  it('drops what is still in flight when disposed', () => {
    const {got, deliver} = collect()
    const link = createLink(deliver, {delayMs: 100, jitterMs: 0, lossPercent: 0})

    link.receive(packet(1))
    link.dispose()
    vi.advanceTimersByTime(1000)

    expect(got).toEqual([])
  })

  it('takes a new condition without being rebuilt', () => {
    const {got, deliver} = collect()
    const link = createLink(deliver, CLEAN)

    link.receive(packet(1))
    link.set({delayMs: 40, jitterMs: 0, lossPercent: 0})
    link.receive(packet(2))

    expect(got).toEqual([1])
    vi.advanceTimersByTime(40)
    expect(got).toEqual([1, 2])
  })
})
