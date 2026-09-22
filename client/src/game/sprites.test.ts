import {readFileSync} from 'node:fs'
import {beforeAll, expect, test} from 'vitest'
import '../../public/wasm_exec.js'

import {advance, initSim, readSnapshot, reset, STATE_NAMES} from '../sim/wasm'
import {IN_HP} from './input'
import {missingTags, tagFor, type SheetMeta} from './sprites'

beforeAll(async () => {
  await initSim(readFileSync('public/main.wasm'))
})

/** The real generated sheets, not a fixture. Relative to the vitest root. */
function sheet(name: string): SheetMeta {
  return JSON.parse(readFileSync(`public/sprites/${name}.json`, 'utf8')) as SheetMeta
}

const ATTACK = STATE_NAMES.indexOf('attack')

test.each(['kai', 'torv'])('%s carries every tag it is asked for', (name) => {
  expect(missingTags(sheet(name))).toEqual([])
})

test.each(['kai', 'torv'])('%s names a tag for every state and every move', (name) => {
  const meta = sheet(name)

  // Every state resolves, attack excepted — its tag comes from the move.
  for (let s = 0; s < STATE_NAMES.length; s++) {
    if (s === ATTACK) continue
    expect(tagFor(meta, s, -1), STATE_NAMES[s]).not.toBe('')
  }

  for (let m = 0; m < meta.meta.moveTags.length; m++) {
    expect(tagFor(meta, ATTACK, m), `move ${m}`).not.toBe('')
  }
})

test('three strengths of one special share an animation, normals do not', () => {
  const meta = sheet('kai')
  const tag = (m: number) => tagFor(meta, ATTACK, m)
  const index = (want: string) => meta.meta.moveTags.indexOf(want)

  // The fireball is one animation at three speeds (D21), so the three move
  // indices land on one tag. Lose this and the sheet needs 20 more animations.
  const fireballs = meta.meta.moveTags.filter((t) => t === '236P')
  expect(fireballs).toHaveLength(3)

  // A normal never collapses: a light punch and a heavy punch are different
  // animations however similarly they are spelled.
  expect(tag(index('5LP'))).toBe('5LP')
  expect(tag(index('5HP'))).toBe('5HP')
  expect(index('5LP')).not.toBe(index('5HP'))
})

test('a missing tag is reported rather than rendered', () => {
  const meta = sheet('kai')
  const broken: SheetMeta = {
    ...meta,
    meta: {...meta.meta, frameTags: meta.meta.frameTags.filter((t) => t.name !== 'jump')},
  }

  // The realistic failure is the boring one: the character JSON grew a move
  // and nobody reran the generator.
  broken.meta.moveTags = [...meta.meta.moveTags, '421K']

  expect(missingTags(broken)).toEqual(['421K', 'jump'])
})

test('a move index the sim uses for no move resolves to nothing, not a crash', () => {
  expect(tagFor(sheet('kai'), ATTACK, -1)).toBe('')
})

test('the real sim, throwing a real heavy punch, lands on the heavy punch tag', () => {
  const meta = sheet('kai')
  reset(false, 0, [0, 0])

  // Held rather than tapped: the press has to survive into an actionable
  // frame, and an attack is six frames against a screenshot's hundreds.
  const seen = new Set<string>()
  for (let f = 0; f < 60; f++) {
    advance(IN_HP, 0)
    const p = readSnapshot().players[0]
    seen.add(tagFor(meta, p.state, p.moveIndex))
  }

  // The state the attack passes through must name the move, not the state.
  expect(seen).toContain('5HP')
  expect(seen).toContain('idle')
  // Nothing observed may be unnamed: an empty tag is a frame with no texture.
  expect([...seen].filter((t) => t === '')).toEqual([])
})

test('every state the sim reaches in a scripted sequence has a texture', () => {
  const meta = sheet('kai')
  reset(false, 0, [0, 0])

  // Walk, jump, crouch, punch — enough to visit the states a match spends
  // most of its frames in, driven by the sim rather than asserted about it.
  const script = [
    [8, 1 << 3], // right: walk_f
    [8, 1 << 2], // left: walk_b
    [10, 1 << 1], // down: crouch
    [30, 1 << 0], // up: prejump, then air
    [20, IN_HP],
  ] as const

  const states = new Set<number>()
  for (const [frames, input] of script) {
    for (let f = 0; f < frames; f++) {
      advance(input, 0)
      const p = readSnapshot().players[0]
      states.add(p.state)
      expect(tagFor(meta, p.state, p.moveIndex), STATE_NAMES[p.state]).not.toBe('')
    }
  }

  // The sequence has to actually visit several states, or the assertion above
  // passed on one idle frame sixty times.
  expect(states.size).toBeGreaterThanOrEqual(5)
})
