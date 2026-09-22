import {readFileSync} from 'node:fs'
import {expect, test} from 'vitest'

import {STATE_NAMES} from '../sim/wasm'
import {missingTags, tagFor, type SheetMeta} from './sprites'

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
