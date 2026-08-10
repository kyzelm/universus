import {Application, Container, Graphics, Text} from 'pixi.js'
import {createNetplay, depthP99, type Netplay} from '../net/netplay'
import type {Peer} from '../net/peer'
import {advance, checksum, loadSim, readSnapshot, reset, rewind} from '../sim/wasm'
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

/** Frames between pings. Two a second is plenty to build an RTT distribution. */
const PING_EVERY = 30

export interface Game {
  /**
   * Switch from local play to netplay over a connected peer. Seat 0 drives P1,
   * seat 1 drives P2; both ends reset to frame 0, and from then on each side
   * simulates frame N from (its own input at N, the other side's input at N).
   */
  connect(peer: Peer, seat: 0 | 1): void
  /** Feed a packet in. The panel owns the channel, so it forwards them here. */
  receive(data: ArrayBuffer): void
  dispose(): void
}

/**
 * Starts the sim, the render loop and the input layer.
 *
 * The view is read-only by construction: it advances the sim, reads a snapshot,
 * and draws. There is no path from here back into sim state.
 */
export async function startGame(parent: HTMLElement): Promise<Game> {
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

  let net: Netplay | null = null
  let ticks = 0

  app.ticker.add((ticker) => {
    for (let i = clock.tick(ticker.deltaMS); i > 0; i--) {
      const [p1, p2] = input.poll()

      const t0 = performance.now()
      if (net) {
        // WASD is always the local player, whichever seat they are in. Which
        // half of the keyboard you use should not depend on who dialled.
        net.step(p1)
        if (++ticks % PING_EVERY === 0) net.ping()
      } else {
        advance(p1, p2)
      }
      stepCost.push(performance.now() - t0)
    }

    const snap = readSnapshot()
    for (let i = 0; i < players.length; i++) {
      const p = snap.players[i]
      players[i].position.set(VIEW_W / 2 + p.x * SCALE, GROUND_PX - p.y * SCALE)
    }

    // ponytail: no interpolation. The sim and the display are both ~60 Hz, so
    // add it when the judder is actually visible, not before.
    if (snap.frame % HUD_EVERY === 0) hud.text = net ? netHud(net) : localHud(snap.frame, stepCost)
  })

  return {
    connect(peer, seat) {
      reset()
      ticks = 0
      net = createNetplay({advance, rewind, checksum}, (data) => peer.send(data), seat)
    },

    receive(data) {
      net?.receive(data)
    },

    dispose() {
      input.dispose()
      // The Go runtime keeps running: quit() cannot be undone and loadSim is
      // cached, so a remount would have no sim. reset() on start covers it.
      app.destroy(true, {children: true})
    },
  }
}

function localHud(frame: number, stepCost: ReturnType<typeof createSamples>): string {
  const ms = (p: number) => stepCost.percentile(p).toFixed(3)
  return `frame ${frame}  sim step p50 ${ms(50)}ms  p99 ${ms(99)}ms  max ${ms(100)}ms`
}

/**
 * The numbers this project exists to report. Percentiles, never means, and
 * stalls and desyncs shown as raw counts because one of either matters.
 */
function netHud(net: Netplay): string {
  const s = net.stats
  const pct = (n: number) => ((n / Math.max(1, s.frames)) * 100).toFixed(1)
  const rtt = (p: number) => (s.rtt.count ? s.rtt.percentile(p).toFixed(1) : '—')

  return [
    `frame ${net.frame}`,
    `rtt p50 ${rtt(50)}ms p99 ${rtt(99)}ms`,
    `rollback ${pct(s.rollbacks)}% depth p99 ${depthP99(s.depths)}`,
    `mispredict ${pct(s.mispredicted)}%`,
    `stalls ${s.stalls}`,
    `checked ${s.verified}`,
    s.desyncs ? `DESYNC at frame ${s.desyncFrame}` : `desync 0`,
  ].join('  ')
}
