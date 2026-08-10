import {expect, test} from 'vitest'
import {createSamples} from './stats'

test('percentiles of a known distribution', () => {
  const s = createSamples(1000)
  for (let i = 1; i <= 100; i++) s.push(i)

  expect(s.count).toBe(100)
  expect(s.percentile(50)).toBe(50)
  expect(s.percentile(99)).toBe(99)
  expect(s.percentile(100)).toBe(100)
  expect(s.percentile(0)).toBe(1)
})

test('order of arrival does not matter', () => {
  const s = createSamples(1000)
  for (const v of [9, 1, 7, 3, 5]) s.push(v)
  expect(s.percentile(50)).toBe(5)
  expect(s.percentile(100)).toBe(9)
})

test('sorts numerically, not lexicographically', () => {
  // Array.prototype.sort's default comparator would put 100 before 9.
  const s = createSamples(10)
  for (const v of [9, 100, 20]) s.push(v)
  expect(s.percentile(100)).toBe(100)
  expect(s.percentile(0)).toBe(9)
})

test('the window rolls, dropping the oldest samples', () => {
  const s = createSamples(10)
  for (let i = 0; i < 10; i++) s.push(1000) // pushed out below
  for (let i = 0; i < 10; i++) s.push(5)

  expect(s.count).toBe(10)
  expect(s.percentile(100)).toBe(5)
})

test('empty is NaN, not zero', () => {
  // Zero would read as "the sim is infinitely fast" on the HUD.
  expect(createSamples().percentile(50)).toBeNaN()
})