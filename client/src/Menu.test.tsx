import {expect, test} from 'vitest'
import {render, screen} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import Menu from './Menu'
import type {RunSetup} from './setup'

const ROSTER = [
  {index: 0, name: 'Shoto'},
  {index: 1, name: 'Grappler'},
]

/**
 * The whole of what the menu is for: a mode and two fighters, handed over as
 * the same setup a URL describes. The second character is unreachable without
 * this screen, which is what makes it worth a check rather than a look.
 */
test('picks a mode and a pair and hands over a setup', async () => {
  const user = userEvent.setup()
  let got: RunSetup | null = null
  render(<Menu roster={ROSTER} onStart={(s) => (got = s)} />)

  await user.click(screen.getByText('versus AI'))
  // Both columns list the roster, so the buttons come in pairs: P1 first.
  const grapplers = screen.getAllByRole('button', {name: 'Grappler'})
  expect(grapplers).toHaveLength(2)
  await user.click(grapplers[1]) // the opponent
  await user.click(screen.getByText('hard'))
  await user.click(screen.getByText('fight'))

  expect(got).toEqual({mode: 'ai', ai: 3, chars: [0, 1]})
})

// The difficulty row belongs to one mode, and a tier carried out of a local
// match would be an AI nobody asked for in seat 2.
test('the difficulty is only offered, and only sent, for an AI match', async () => {
  const user = userEvent.setup()
  let got: RunSetup | null = null
  render(<Menu roster={ROSTER} onStart={(s) => (got = s)} />)

  await user.click(screen.getByText('local versus'))
  expect(screen.queryByText('hard')).toBeNull()
  await user.click(screen.getByText('fight'))

  expect(got).toEqual({mode: 'local', ai: 0, chars: [0, 0]})
})
