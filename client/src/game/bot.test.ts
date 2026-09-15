import {expect, test} from 'vitest'
import {createBot} from './bot'

const RESERVED = 0b1111_1000_1000_0000

function run(seed: number, frames = 600): number[] {
  const bot = createBot(seed)
  return Array.from({length: frames}, () => bot.poll())
}

test('the same seed replays the same inputs, and a different seat does not', () => {
  expect(run(1)).toEqual(run(1))
  expect(run(1)).not.toEqual(run(2))
})

test('never sets a reserved bit', () => {
  for (const bits of run(1)) expect(bits & RESERVED).toBe(0)
})

test('changes often enough to mispredict — the reason it exists', () => {
  const frames = run(1)
  const changes = frames.filter((bits, i) => i > 0 && bits !== frames[i - 1]).length

  // Prediction repeats the last input, so a change is the only chance of a
  // rollback. Once a second would be a measurement of nothing.
  expect(changes).toBeGreaterThan(frames.length / 10)
})
