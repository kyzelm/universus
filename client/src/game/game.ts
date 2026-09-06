import {Application, Container, Graphics, Text} from 'pixi.js'
import {CLEAN, createLink, type Impairment, type Link} from '../net/impair'
import {createNetplay, depthP99, type Netplay} from '../net/netplay'
import type {Peer} from '../net/peer'
import {
  advance,
  dataVersion,
  type Box,
  checksum,
  EVENT_BLOCK,
  EVENT_HIT,
  EVENT_KNOCKDOWN,
  EVENT_SUPER,
  EVENT_THROWN,
  eventsAt,
  loadSim,
  type PlayerSnapshot,
  readSnapshot,
  BAR_UNITS,
  COUNTER_NAMES,
  DRIVE_BARS,
  NOBODY,
  PHASE_FIGHT,
  PHASE_INTRO,
  PHASE_KO,
  PHASE_MATCH_END,
  SUPER_BARS,
  reset,
  rewind,
  type Snapshot,
  STATE_NAMES,
} from '../sim/wasm'
import {createClock, STEP_MS} from './clock'
import {createEvents} from './events'
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

/**
 * Effect colours, one per event flag. The spark is a placeholder for the sounds
 * and particles that arrive in M4 — what matters here is that it is fired
 * through the confirmed-frame path, so everything hung off that path later
 * inherits a mechanism that has already been proven not to double-fire.
 */
const EVENT_COLORS: readonly (readonly [number, number])[] = [
  [EVENT_HIT, 0xffe08a],
  [EVENT_BLOCK, 0x8ac8ff],
  [EVENT_THROWN, 0xffa84a],
  [EVENT_KNOCKDOWN, 0xff7a4a],
  [EVENT_SUPER, 0xd08aff],
]

/** How long a spark lives, in display frames. */
const SPARK_FRAMES = 10
const SPARK_RADIUS = 14

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
  /**
   * Sets the artificial network conditions applied to arriving packets. The
   * measurement matrix is run by calling this, not by rebuilding anything.
   */
  impair(cfg: Impairment): void
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

  // Sparks live in the world, not on the stage: an effect at a fighter's feet
  // has to scroll with the fighter.
  const sparkLayer = new Graphics()
  world.addChild(sparkLayer)

  const bars = [new Graphics(), new Graphics()]
  bars.forEach((b) => app.stage.addChild(b))

  // The combo readout, one per seat. A counter hit that is not visible is one
  // nobody learns from, so it is drawn every frame rather than on the HUD's
  // slower clock.
  const combos = [0, 1].map((seat) => {
    const t = new Text({
      text: '',
      style: {fill: 0xe0c04a, fontFamily: 'monospace', fontSize: 14, fontWeight: 'bold'},
    })
    t.position.set(seat === 0 ? 12 : VIEW_W - 12, 48)
    t.anchor.set(seat === 0 ? 0 : 1, 0)
    app.stage.addChild(t)
    return t
  })

  // The round clock and the announcement. Both read the sim's own phase and
  // timer: a view that counted its own seconds would show a different number
  // from the one the round actually ends on.
  const timer = new Text({
    text: '',
    style: {fill: 0xe8ecf2, fontFamily: 'monospace', fontSize: 26, fontWeight: 'bold'},
  })
  timer.position.set(VIEW_W / 2, 6)
  timer.anchor.set(0.5, 0)
  app.stage.addChild(timer)

  const announce = new Text({
    text: '',
    style: {fill: 0xe8ecf2, fontFamily: 'monospace', fontSize: 30, fontWeight: 'bold'},
  })
  announce.position.set(VIEW_W / 2, 150)
  announce.anchor.set(0.5, 0.5)
  app.stage.addChild(announce)

  const hud = new Text({
    text: '',
    style: {fill: 0x8a94a6, fontFamily: 'monospace', fontSize: 12},
  })
  hud.position.set(8, 52)
  app.stage.addChild(hud)

  const input = createInput()
  const clock = createClock()
  let stepCost = createSamples()

  // Wall-clock time between displayed frames. The sim step is measured
  // separately above; this is the number the 60 fps bar is actually about,
  // because a frame that fits the budget and is still displayed late is a
  // dropped frame to the player.
  let frameCost = createSamples()
  let longFrames = 0

  let net: Netplay | null = null
  let link: Link | null = null
  let ticks = 0

  // Every playtest is a free regression log, and the corpus is worth more than
  // any single test in here — but only if it is actually recorded. Growth is
  // ~864 KB an hour, which is not worth a ring buffer.
  //
  // Local play only. Under netplay the driver decides what the sim is fed,
  // including on replays, so the log that is honest about a match is the one
  // it keeps of confirmed frames — see inputLog.
  const recorded: number[] = []

  // **Effects fire from state, on confirmed frames, and never from inside the
  // sim.** The sim sets flags and fires nothing; this drains them for frames
  // that can no longer be simulated again. Built in M2 with a placeholder
  // spark attached, because retrofitting it once there are real sounds means
  // auditing every effect in the game (02 Architecture/Rollback Netcode.md).
  const pump = createEvents(eventsAt)
  const sparks: {x: number; y: number; life: number; color: number}[] = []

  app.ticker.add((ticker) => {
    frameCost.push(ticker.deltaMS)
    // A frame that took longer than one and a half steps is one the display
    // did not get. Counted rather than averaged: under continuous rollback the
    // question is whether any frame was missed, not what the mean was.
    if (ticker.deltaMS > STEP_MS * 1.5) longFrames++

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

    // Under netplay the confirmed line lags the simulated frame by the
    // rollback window; locally every frame that has run is settled the moment
    // it has. Either way nothing is drawn for a frame that could still change.
    pump.drain(net ? net.confirmed : snap.frame - 1, ({seat, bits}) => {
      const p = snap.players[seat]
      for (const [bit, color] of EVENT_COLORS) {
        // ponytail: drawn where the fighter is now, not where they were on the
        // event's own frame. That is at most eight frames of drift and the
        // alternative is a position history the view has no other use for.
        if (bits & bit) sparks.push({x: p.x, y: p.y, life: SPARK_FRAMES, color})
      }
    })

    sparkLayer.clear()
    for (let i = sparks.length - 1; i >= 0; i--) {
      const s = sparks[i]
      const t = s.life / SPARK_FRAMES
      sparkLayer
        .circle(s.x * SCALE, GROUND_PX - (s.y + 24) * SCALE, SPARK_RADIUS * (1.4 - t))
        .stroke({color: s.color, width: 2, alpha: t})
      if (--s.life <= 0) sparks.splice(i, 1)
    }

    boxes.clear()
    for (const p of snap.players) drawBoxes(boxes, p)
    // A projectile is a hitbox with no character attached, so it is drawn as
    // one: the overlay's job is to show what can hit you.
    for (const b of snap.projectiles) drawHitbox(boxes, b)
    setText(timer, `${seconds(snap.timer)}`)
    setText(announce, announcement(snap))

    for (let i = 0; i < bars.length; i++) {
      drawBars(bars[i], snap.players[i], i, snap.frame, snap.wins[i])
      // The combo belongs to the player taking it; it is shown on the side of
      // the player landing it, which is where every game in the genre puts it.
      setCombo(combos[1 - i], snap.players[i])
    }

    // ponytail: no interpolation. The sim and the display are both ~60 Hz, so
    // add it when the judder is actually visible, not before.
    if (snap.frame % HUD_EVERY === 0) {
      const top = net ? netHud(net, link) : localHud(snap)
      hud.text = `${top}\n${costHud(stepCost, frameCost, longFrames)}`
    }
  })

  return {
    inputLog() {
      // Under netplay the driver's log is the authority: it holds the inputs
      // that were *confirmed*, which is what the match actually simulated once
      // every rollback had been applied. A log kept out here would record the
      // predictions instead, and replay a match nobody played.
      const src = net ? net.log : recorded

      const buf = new ArrayBuffer(src.length * 2)
      const v = new DataView(buf)
      // Explicit little-endian rather than a Uint16Array, which would use the
      // platform's byte order and silently produce a different file elsewhere.
      for (let i = 0; i < src.length; i++) v.setUint16(i * 2, src[i], true)
      return new Blob([buf], {type: 'application/octet-stream'})
    },

    connect(peer, seat) {
      reset()
      // The match restarts at frame 0, so what has been fired restarts with it.
      pump.reset()
      sparks.length = 0
      ticks = 0
      longFrames = 0
      net = createNetplay({advance, rewind, checksum, dataVersion}, (data) => peer.send(data), seat)
      // Every arriving packet goes through the impairment layer, whatever it
      // is set to — the clean setting passes straight through, so a real match
      // runs the same path a measured one does.
      link = createLink((data) => net?.receive(data), CLEAN)
      net.hello()
    },

    receive(data) {
      if (link) link.receive(data)
      else net?.receive(data)
    },

    impair(cfg) {
      link?.set(cfg)
      // The cost numbers belong to the condition that produced them. Rollback
      // depth changes what a frame costs, so a cell measured on the previous
      // cell's samples is a cell reporting the wrong thing.
      stepCost = createSamples()
      frameCost = createSamples()
      longFrames = 0
      net?.resetStats()
    },

    dispose() {
      input.dispose()
      link?.dispose()
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
  for (const b of p.hitboxes) drawHitbox(g, b)
}

function drawHitbox(g: Graphics, b: Box): void {
  g.rect(...toScreen(b)).fill({color: HIT_COLOR, alpha: 0.3}).stroke({color: HIT_COLOR, width: 1})
}

const BAR_W = 340
const MAX_HEALTH = 10000

/** Pips drawn per seat. Mirrors balance.json's round.roundsToWin. */
const ROUNDS_TO_WIN = 2

const EMPTY_COLOR = 0x2a2f38
const WIN_COLOR = 0xe8ecf2
const HEALTH_COLOR = 0xd8c15a
const DRIVE_COLOR = 0x4ac8e0
const BURNOUT_COLOR = 0xe07a2a
const SUPER_COLOR = 0xc86ee0

/**
 * Health, then the two resources. They are the most-read elements on screen
 * after the fighters, and Burnout has to be readable at a glance: the Drive
 * gauge changes colour and pulses, so a supervisor watching a demo can see the
 * resource system working without being told.
 */
function drawBars(
  g: Graphics,
  p: PlayerSnapshot,
  seat: number,
  frame: number,
  wins: number,
): void {
  const x = seat === 0 ? 10 : VIEW_W - 10 - BAR_W

  g.clear()
  g.rect(x, 8, BAR_W, 14).fill(EMPTY_COLOR)
  // Drains from the centre outward, so both bars empty toward the middle.
  const w = BAR_W * clamp01(p.health / MAX_HEALTH)
  g.rect(seat === 0 ? x + BAR_W - w : x, 8, w, 14).fill(HEALTH_COLOR)

  const burnout = p.burnout !== 0
  drawGauge(g, x, 25, 9, seat, p.drive, DRIVE_BARS, {
    color: burnout ? BURNOUT_COLOR : DRIVE_COLOR,
    // A slow pulse, driven by the sim's frame so it cannot drift from the
    // state it is reporting.
    alpha: burnout ? 0.55 + 0.45 * Math.sin(frame / 6) : 1,
  })
  drawGauge(g, x, 37, 7, seat, p.super, SUPER_BARS, {color: SUPER_COLOR, alpha: 1})

  // Round wins, as pips beside the health bar. Two of them takes the match, so
  // there is never a number worth writing out.
  for (let i = 0; i < ROUNDS_TO_WIN; i++) {
    const px = seat === 0 ? x + BAR_W + 6 + i * 12 : x - 12 - i * 12
    g.circle(px, 15, 4).fill(i < wins ? WIN_COLOR : EMPTY_COLOR)
  }
}

/**
 * One segmented gauge. The segments are the point: a bar count is what the
 * player reads, since every Drive mechanic is priced in whole or half bars.
 */
function drawGauge(
  g: Graphics,
  x: number,
  y: number,
  h: number,
  seat: number,
  value: number,
  bars: number,
  style: {color: number; alpha: number},
): void {
  const gap = 2
  const segW = (BAR_W - gap * (bars - 1)) / bars

  for (let i = 0; i < bars; i++) {
    // Both gauges fill away from the outside edge of the screen, like health.
    const index = seat === 0 ? bars - 1 - i : i
    const sx = x + index * (segW + gap)
    const filled = clamp01(value / BAR_UNITS - i) * segW

    g.rect(sx, y, segW, h).fill(EMPTY_COLOR)
    g.rect(seat === 0 ? sx + segW - filled : sx, y, filled, h).fill(style)
  }
}

/**
 * The round clock in whole seconds, rounded up: the sim counts frames, and the
 * view is the only place allowed to hold a number that is not exact.
 */
function seconds(frames: number): number {
  return Math.ceil((frames * STEP_MS) / 1000)
}

/**
 * What is written across the middle of the screen. Driven by the phase the sim
 * is in rather than by anything the view worked out for itself — the sim is
 * what decides a round is over, and the two must not disagree by a frame.
 */
function announcement(snap: Snapshot): string {
  const who = (seat: number) => `PLAYER ${seat + 1}`

  switch (snap.phase) {
    case PHASE_FIGHT:
      return ''
    case PHASE_KO:
      // A drawn round ended either on a double KO or on the clock; both read
      // as a draw, and the distinction is not one the player needs.
      if (snap.roundWinner === NOBODY) return 'DRAW'
      return snap.timer === 0 ? 'TIME UP' : 'K.O.'
    case PHASE_INTRO:
      return 'FIGHT'
    case PHASE_MATCH_END:
      return `${who(snap.winner)} WINS THE MATCH`
    default:
      return snap.roundWinner === NOBODY ? 'DRAW' : `${who(snap.roundWinner)} WINS THE ROUND`
  }
}

/** Assigning re-lays out the text, so only do it when it actually changed. */
function setText(t: Text, text: string): void {
  if (t.text !== text) t.text = text
}

function clamp01(v: number): number {
  return Math.max(0, Math.min(1, v))
}

/** Reads as "COUNTER · 3 hits", or nothing at all outside a combo. */
function setCombo(t: Text, defender: PlayerSnapshot): void {
  const parts = []
  if (defender.combo > 0) {
    if (COUNTER_NAMES[defender.counter]) parts.push(COUNTER_NAMES[defender.counter])
    parts.push(`${defender.combo} hit${defender.combo === 1 ? '' : 's'}`)
  }
  setText(t, parts.join(' · '))
}

function localHud(snap: Snapshot): string {
  const who = (i: number) => {
    const p = snap.players[i]
    const drive = (p.drive / BAR_UNITS).toFixed(1)
    const meter = (p.super / BAR_UNITS).toFixed(1)
    return (
      `p${i} ${STATE_NAMES[p.state] ?? p.state}:${p.stateFrame} hp ${p.health} ` +
      `drive ${drive}${p.burnout ? ' BURNOUT' : ''} super ${meter}` +
      (p.combo ? ` combo ${p.combo}` : '')
    )
  }
  return [
    `frame ${snap.frame}`,
    snap.hitstop ? `HITSTOP ${snap.hitstop}` : '',
    who(0),
    who(1),
  ]
    .filter(Boolean)
    .join('  ')
}

/**
 * The cost line, shown in both modes. Under netplay the sim figure includes
 * every rollback replay the frame ran, which is the measurement M-A asks for:
 * a frame with an 8-frame rollback runs the sim nine times and still has to fit
 * 16.6 ms. `long` counts frames the display did not get at all — the number the
 * "sustained 60 fps under continuous rollback" bar is really about, since a
 * frame can fit the budget and still be shown late.
 */
function costHud(
  stepCost: ReturnType<typeof createSamples>,
  frameCost: ReturnType<typeof createSamples>,
  longFrames: number,
): string {
  const sim = (p: number) => stepCost.percentile(p).toFixed(3)
  const frame = (p: number) => frameCost.percentile(p).toFixed(1)

  return (
    `sim p50 ${sim(50)}ms p99 ${sim(99)}ms  ` +
    `frame p50 ${frame(50)}ms p99 ${frame(99)}ms  long ${longFrames}`
  )
}

/**
 * The numbers this project exists to report. Percentiles, never means, and
 * stalls and desyncs shown as raw counts because one of either matters.
 */
function netHud(net: Netplay, link: Link | null): string {
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
    // Ahead and behind read differently and both matter: the leading end is
    // the one that skips, and a skip count that never moves on a laggy link
    // means time synchronisation is not doing its job.
    `ahead ${net.advantage > 0 ? '+' : ''}${net.advantage}`,
    `stalls ${s.stalls}`,
    `skips ${s.skipped}`,
    `checked ${s.verified}`,
    s.desyncs ? `DESYNC at frame ${s.desyncFrame}` : `desync 0`,
    conditions(link),
  ]
    .filter(Boolean)
    .join('  ')
}

/**
 * The artificial conditions in force, and what they actually did. Silent when
 * the layer is clean, because a line that is always there stops being read —
 * and every number above it means something different once this one appears.
 */
function conditions(link: Link | null): string {
  if (!link) return ''
  const {delayMs, jitterMs, lossPercent} = link.cfg
  if (delayMs <= 0 && jitterMs <= 0 && lossPercent <= 0) return ''

  const {arrived, dropped} = link.stats
  const rate = ((dropped / Math.max(1, arrived + dropped)) * 100).toFixed(1)
  return `SIM-NET +${delayMs}±${jitterMs}ms ${lossPercent}% loss (dropped ${dropped}, ${rate}%)`
}
