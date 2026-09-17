import {expect, test} from 'vitest'
import {setupFromURL} from './setup'

/**
 * A bare URL is somebody arriving and gets the menu. **Everything else has to
 * land in the game**, including the parameters nobody types: a measurement
 * harness, a CI run and a link somebody was sent all arrive with one, and a
 * title screen waiting to be clicked would hang every one of them.
 */
test('a bare URL is the only one that gets the menu', () => {
  expect(setupFromURL('')).toBeNull()
  expect(setupFromURL('?sweep')).toBeNull() // its own page, not a run

  for (const search of ['?bot', '?bench', '?room=ABC', '?training', '?ai=hard', '?p1=1']) {
    expect(setupFromURL(search), search).not.toBeNull()
  }
})

test('the URL names the same setup the menu builds', () => {
  expect(setupFromURL('?training&p2=1')).toEqual({mode: 'training', ai: 0, chars: [0, 1]})
  expect(setupFromURL('?ai=hard&p1=1&p2=1')).toEqual({mode: 'ai', ai: 3, chars: [1, 1]})
  expect(setupFromURL('?room=ABC')).toEqual({mode: 'online', ai: 0, chars: [0, 0]})
  expect(setupFromURL('?bot')).toEqual({mode: 'local', ai: 0, chars: [0, 0]})

  // Training wins over a difficulty, which is the precedence the sim had
  // before the menu existed: the lab is a mode, not an opponent.
  expect(setupFromURL('?training&ai=hard')?.mode).toBe('training')

  // A character index that is not one, or not there at all, is the first entry
  // rather than a fighter who cannot move. The roster bound is checked later,
  // where the roster is actually loaded.
  expect(setupFromURL('?p1=-3&p2=x')?.chars).toEqual([0, 0])
})
