import {Application, Container, Graphics, Text} from 'pixi.js'
import {createNetplay, depthP99, type Netplay} from '../net/netplay'
import type {Peer} from '../net/peer'
import {
  advance,
  dataVersion,
  type Box,
  checksum,
  loadSim,
  type PlayerSnapshot,
  readSnapshot,
  reset,
  rewind,
  STATE_NAMES,
} from '../sim/wasm'
import {createClock} from './clock'
import {createInput} from './input'
import {createSamples} from './stats'

const VIEW_W = 800
const VIEW_H = 450

/** Pixels per sim unit. The stage is ~400 units wide, so 2 fills the view. */
const SCALE = 2
const GROUND_PX = 380

const HUD_EVERY = 30

/**
 * Debug box colours, the convention every fighting game debug view uses:
 * blue pushbox, green hurtbox, red hitbox.
 *
 * This is the tool that debugs every system built on top of boxes, which is why
 * it exists before there is anything to look at. It draws the boxes the sim
 * actually collides, carried in the snapshot — boxes recomputed here would
 * agree with themselves and disagree with the game.
 */
const PUSH_COLOR = 0x4a8ce0
const HURT_COLOR = 0x4ae08c
const HIT_COLOR = 0xe0574a

/** Frames between pings. Two a second is plenty to build an RTT distribution. */
const PING_EVERY = 30

export interface Game {
  /**
   * The inputs fed to the sim so far, in the replay log format: little-endian
   * uint16 pairs, player 1 then player 2, one pair per frame, no header. The
   * same bytes tools/replay reads.
   */
  inputLog(): Blob
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

  // The world scrolls under a fixed camera; the stage layer does not.
  const world = new Container()
  app.stage.addChild(world)

  world.addChild(new Graphics().rect(-2000, GROUND_PX, 4000, VIEW_H - GROUND_PX).fill(0x2a2f38))

  // Wall markers, so the corner is visible as a place rather than a surprise.
  for (const side of [-1, 1]) {
    world.addChild(
      new Graphics().rect(side * 200 * SCALE - 2, 0, 4, GROUND_PX).fill(0x3a4150),
    )
  }

  const boxes = new Graphics()
  world.addChild(boxes)

  const bars = [new Graphics(), new Graphics()]
  bars.forEach((b) => app.stage.addChild(b))

  const hud = new Text({
    text: '',
    style: {fill: 0x8a94a6, fontFamily: 'monospace', fontSize: 12},
  })
  hud.position.set(8, 30)
  app.stage.addChild(hud)

  const input = createInput()
  const clock = createClock()
  const stepCost = createSamples()

  let net: Netplay | null = null
  let ticks = 0

  // Every playtest is a free regression log, and the corpus is worth more than
  // any single test in here — but only if it is actually recorded. Growth is
  // ~864 KB an hour, which is not worth a ring buffer.
  //
  // Local play only: under netplay the driver decides what the sim is fed,
  // including on replays, so the honest log lives there rather than here.
  const recorded: number[] = []

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
        recorded.push(p1, p2)
      }
      stepCost.push(performance.now() - t0)
    }

    const snap = readSnapshot()

    // The camera is sim state, not a view decision — corner position is
    // gameplay, so both machines must agree on where the corner is.
    world.position.x = VIEW_W / 2 - snap.camX * SCALE

    boxes.clear()
    for (const p of snap.players) drawBoxes(boxes, p)
    for (let i = 0; i < bars.length; i++) drawHealth(bars[i], snap.players[i], i)

    // ponytail: no interpolation. The sim and the display are both ~60 Hz, so
    // add it when the judder is actually visible, not before.
    if (snap.frame % HUD_EVERY === 0) {
      hud.text = net ? netHud(net) : localHud(snap, stepCost)
    }
  })

  return {
    inputLog() {
      const buf = new ArrayBuffer(recorded.length * 2)
      const v = new DataView(buf)
      // Explicit little-endian rather than a Uint16Array, which would use the
      // platform's byte order and silently produce a different file elsewhere.
      for (let i = 0; i < recorded.length; i++) v.setUint16(i * 2, recorded[i], true)
      return new Blob([buf], {type: 'application/octet-stream'})
    },

    connect(peer, seat) {
      reset()
      ticks = 0
      net = createNetplay({advance, rewind, checksum, dataVersion}, (data) => peer.send(data), seat)
      net.hello()
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

/** Sim coordinates are feet-origin, y up; the canvas is y down. */
function toScreen(b: Box): [number, number, number, number] {
  return [b.x * SCALE, GROUND_PX - (b.y + b.h) * SCALE, b.w * SCALE, b.h * SCALE]
}

function drawBoxes(g: Graphics, p: PlayerSnapshot): void {
  g.rect(...toScreen(p.pushbox)).stroke({color: PUSH_COLOR, width: 1})
  for (const b of p.hurtboxes) {
    g.rect(...toScreen(b)).fill({color: HURT_COLOR, alpha: 0.2}).stroke({color: HURT_COLOR, width: 1})
  }
  for (const b of p.hitboxes) {
    g.rect(...toScreen(b)).fill({color: HIT_COLOR, alpha: 0.3}).stroke({color: HIT_COLOR, width: 1})
  }
}

const BAR_W = 340
const MAX_HEALTH = 10000

function drawHealth(g: Graphics, p: PlayerSnapshot, seat: number): void {
  const x = seat === 0 ? 10 : VIEW_W - 10 - BAR_W
  const frac = Math.max(0, Math.min(1, p.health / MAX_HEALTH))
  const w = BAR_W * frac

  g.clear()
  g.rect(x, 8, BAR_W, 14).fill(0x2a2f38)
  // Drains from the centre outward, so both bars empty toward the middle.
  g.rect(seat === 0 ? x + BAR_W - w : x, 8, w, 14).fill(0xd8c15a)
}

function localHud(
  snap: ReturnType<typeof readSnapshot>,
  stepCost: ReturnType<typeof createSamples>,
): string {
  const ms = (p: number) => stepCost.percentile(p).toFixed(3)
  const who = (i: number) => {
    const p = snap.players[i]
    return `p${i} ${STATE_NAMES[p.state] ?? p.state}:${p.stateFrame} hp ${p.health}`
  }
  return [
    `frame ${snap.frame}`,
    snap.hitstop ? `HITSTOP ${snap.hitstop}` : '',
    who(0),
    who(1),
    `sim p50 ${ms(50)}ms p99 ${ms(99)}ms`,
  ]
    .filter(Boolean)
    .join('  ')
}

/**
 * The numbers this project exists to report. Percentiles, never means, and
 * stalls and desyncs shown as raw counts because one of either matters.
 */
function netHud(net: Netplay): string {
  const s = net.stats
  if (s.dataMismatch) {
    return 'REFUSED: the peer has different character data. Rebuild both ends from the same commit.'
  }
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
