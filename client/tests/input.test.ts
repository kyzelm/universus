import {expect, test} from 'vitest'
import {
  createInput,
  IN_DOWN,
  IN_LEFT,
  IN_RIGHT,
  IN_UP,
  packBits,
  resolveSOCD,
} from '../src/game/input'

test('SOCD: left and right resolve to neutral', () => {
  expect(resolveSOCD(IN_LEFT | IN_RIGHT)).toBe(0)
  expect(resolveSOCD(IN_LEFT | IN_RIGHT | IN_UP)).toBe(IN_UP)
})

test('SOCD: up beats down', () => {
  expect(resolveSOCD(IN_UP | IN_DOWN)).toBe(IN_UP)
})

test('SOCD: every combination of the four directions is stable', () => {
  for (let bits = 0; bits < 16; bits++) {
    const once = resolveSOCD(bits)
    expect(resolveSOCD(once)).toBe(once)
    // A resolved bitfield never holds an opposing pair.
    expect(once & IN_LEFT && once & IN_RIGHT).toBeFalsy()
    expect(once & IN_UP && once & IN_DOWN).toBeFalsy()
  }
})

test('SOCD: uncontested directions pass through untouched', () => {
  for (const bit of [IN_UP, IN_DOWN, IN_LEFT, IN_RIGHT]) {
    expect(resolveSOCD(bit)).toBe(bit)
  }
})

test('packBits reads only the keys it was given', () => {
  const held = new Set(['KeyA', 'ArrowRight'])
  expect(packBits(held, {KeyA: IN_LEFT})).toBe(IN_LEFT)
  expect(packBits(held, {ArrowRight: IN_RIGHT})).toBe(IN_RIGHT)
  expect(packBits(held, {KeyW: IN_UP})).toBe(0)
})

function press(t: EventTarget, code: string) {
  t.dispatchEvent(new KeyboardEvent('keydown', {code, cancelable: true}))
}

function release(t: EventTarget, code: string) {
  t.dispatchEvent(new KeyboardEvent('keyup', {code}))
}

test('poll reports held keys per player', () => {
  const target = new EventTarget()
  const input = createInput(target)

  expect(input.poll()).toEqual([0, 0])

  press(target, 'KeyD')
  press(target, 'ArrowLeft')
  expect(input.poll()).toEqual([IN_RIGHT, IN_LEFT])

  // Held state persists across polls — the loop reads it every fixed step.
  expect(input.poll()).toEqual([IN_RIGHT, IN_LEFT])

  release(target, 'KeyD')
  expect(input.poll()).toEqual([0, IN_LEFT])

  input.dispose()
})

test('a stuck key is released when focus is lost', () => {
  const target = new EventTarget()
  const input = createInput(target)

  press(target, 'KeyA')
  expect(input.poll()).toEqual([IN_LEFT, 0])

  target.dispatchEvent(new Event('blur'))
  expect(input.poll()).toEqual([0, 0])

  input.dispose()
})

test('dispose stops tracking', () => {
  const target = new EventTarget()
  const input = createInput(target)
  input.dispose()

  press(target, 'KeyA')
  expect(input.poll()).toEqual([0, 0])
})