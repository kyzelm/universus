import {Application, Container, Graphics, Text} from 'pixi.js'
import {advance, loadSim, readSnapshot, reset} from '../utils/wasm'
import {createClock} from './clock'
import {createInput} from './input'
import {createSamples} from './stats'

const VIEW_W = 800
const VIEW_H = 450

/** Pixels per sim unit. The stage is ~400 units wide, so 2 fills the view. */
const SCALE = 2
const GROUND_PX = 380

// Mirrors the pushbox constants in sim/state.go. M1 reads these from character
// JSON along with everything else.
const PLAYER_W = 24 * SCALE
const PLAYER_H = 48 * SCALE

const HUD_EVERY = 30

/**
 * Starts the sim, the render loop and the input layer. Returns a dispose
 * function; call it on unmount.
 *
 * The view is read-only by construction: it calls advance, reads a snapshot,
 * and draws. There is no path from here back into sim state.
 */
export async function startGame(parent: HTMLElement): Promise<() => void> {
  await loadSim()
  reset()

  const app = new Application()
  await app.init({width: VIEW_W, height: VIEW_H, background: 0x14161a, antialias: false})
  parent.appendChild(app.canvas)

  const world = new Container()
  app.stage.addChild(world)

  world.addChild(new Graphics().rect(0, GROUND_PX, VIEW_W, VIEW_H - GROUND_PX).fill(0x2a2f38))

  // Feet-centred, so the sprite origin is the sim position.
  const players = [0xe0574a, 0x4a8ce0].map((color) => {
    const g = new Graphics().rect(-PLAYER_W / 2, -PLAYER_H, PLAYER_W, PLAYER_H).fill(color)
    world.addChild(g)
    return g
  })

  const hud = new Text({
    text: '',
    style: {fill: 0x8a94a6, fontFamily: 'monospace', fontSize: 12},
  })
  hud.position.set(8, 8)
  app.stage.addChild(hud)

  const input = createInput()
  const clock = createClock()
  const stepCost = createSamples()

  app.ticker.add((ticker) => {
    for (let i = clock.tick(ticker.deltaMS); i > 0; i--) {
      const [p1, p2] = input.poll()

      const t0 = performance.now()
      advance(p1, p2)
      stepCost.push(performance.now() - t0)
    }

    const snap = readSnapshot()
    for (let i = 0; i < players.length; i++) {
      const p = snap.players[i]
      players[i].position.set(VIEW_W / 2 + p.x * SCALE, GROUND_PX - p.y * SCALE)
    }

    // ponytail: no interpolation. The sim and the display are both ~60 Hz, so
    // add it when the judder is actually visible, not before.
    if (snap.frame % HUD_EVERY === 0) {
      const ms = (p: number) => stepCost.percentile(p).toFixed(3)
      hud.text = `frame ${snap.frame}  sim step p50 ${ms(50)}ms  p99 ${ms(99)}ms  max ${ms(100)}ms`
    }
  })

  return () => {
    input.dispose()
    // The Go runtime keeps running: quit() cannot be undone and loadSim is
    // cached, so a remount would have no sim. reset() on start covers it.
    app.destroy(true, {children: true})
  }
}
