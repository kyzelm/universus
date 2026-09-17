import {Application, Container, Graphics, Text} from 'pixi.js'
import {CLEAN, createLink, type Impairment, type Link} from '../net/impair'
import {createNetplay, depthP99, MAX_ROLLBACK, TIMELINE, type Netplay} from '../net/netplay'
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
  numCharacters,
  PHASE_FIGHT,
  PHASE_INTRO,
  PHASE_KO,
  PHASE_MATCH_END,
  SUPER_BARS,
  reset,
  rewind,
  type Setup,
  type Snapshot,
  STATE_NAMES,
} from '../sim/wasm'
import type {Bot} from './bot'
import {encodeLog} from './log'
import {uploadResult} from '../net/report'
import {createClock, STEP_MS} from './clock'
import {createEvents} from './events'
import {createDummy, type DummyMode} from './dummy'
import {createInput} from './input'
import {advantageText, createLab} from './lab'
import {createSamples, type Samples} from './stats'

const VIEW_W = 800
const VIEW_H = 450

/** Pixels per sim unit. The stage is ~400 units wide, so 2 fills the view. */
const SCALE = 2
const GROUND_PX = 380

/**
 * Displayed frames between HUD refreshes, **counted off the display and not
 * off the sim frame.** A stalled sim holds its frame number still, so gating
 * on `frame % HUD_EVERY` freezes the HUD on whatever text it last drew — and
 * a stall is exactly when the stall, skip and RTT counters are worth reading.
 */
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

/**
 * The rollback visualiser (01 Thesis/Supervisor Deliverables.md). **Rollback is
 * invisible when it works** — a match over a bad connection looks exactly like
 * a match over a perfect one, which means the hardest part of the project looks
 * like nothing happened. The strip is three seconds of frames, coloured by what
 * each was simulated from, with a tick where each correction landed and how
 * deep it went.
 */
const STRIP_COL = 2
const STRIP_H = 12
const STRIP_DEPTH_H = 22
const STRIP_X = 10
const STRIP_Y = VIEW_H - 32

/** Clean, predicted, corrected — indexed by the FRAME_* constants. */
const STRIP_COLORS = [0x3a7a4a, 0xd8a53a, 0xe0574a]


export interface Game {
  /**
   * The session so far in the replay log format: a 16-byte header carrying the
   * setup, then little-endian uint16 pairs, player 1 then player 2, one pair
   * per frame. The same bytes tools/replay reads.
   */
  inputLog(): Blob
  /**
   * Switch from local play to netplay over a connected peer. Seat 0 drives P1,
   * seat 1 drives P2; both ends reset to frame 0, and from then on each side
   * simulates frame N from (its own input at N, the other side's input at N).
   */
  connect(peer: Peer, seat: 0 | 1, pair?: [number, number], matchID?: number): void
  /** Feed a packet in. The panel owns the channel, so it forwards them here. */
  receive(data: ArrayBuffer): void
  /** True in the lab. The net panel reads it and refuses to connect. */
  readonly training: boolean
  /**
   * The characters this run was started with. The panel hands them to the
   * room, where the host's pair becomes the pair both ends play.
   */
  readonly chars: [number, number]
  /** Starts the match over, in the same mode. The training reset key. */
  restart(): void
  /**
   * Picks what the dummy does with seat 2. Training only; 'manual' hands the
   * seat back to its keyboard, which is where it starts.
   */
  setDummy(mode: DummyMode): void
  /**
   * The figures the HUD draws, as plain numbers, for the measurement harness
   * (01 Thesis/Measurement Methodology.md). A run whose numbers were read off
   * a screenshot by eye cannot be re-read, checked or plotted later.
   *
   * Read-only and one-way: nothing in the game reads this back, and no sim
   * state is exposed through it, so it can never become a path into the sim.
   */
  measure(): Record<string, unknown>
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
 *
 * `bot` replaces the local player's device for a measurement run. It sits
 * where the keyboard sits, so the sim cannot tell the difference and neither
 * can the replay log.
 *
 * `training` starts the match in the lab (03 Game Design/Game Modes.md):
 * resources refill, the clock stops, the round never ends. It is a mode of the
 * *match* and lives in sim state, so it is offline by construction — the panel
 * refuses to connect in it.
 *
 * `ai` hands seat 2 to the scripted opponent at that difficulty tier
 * (03 Game Design/AI Opponent.md). Also sim state, and for a stronger reason:
 * the AI's presses are generated inside the sim, so there is nothing out here
 * that could produce them and nothing out here that may disagree about them.
 */
export async function startGame(
  parent: HTMLElement,
  bot?: Bot,
  training = false,
  ai = 0,
  chars: [number, number] = [0, 0],
): Promise<Game> {
  await loadSim()
  // The characters arrive from the URL, so they are user input and clamped
  // here — the first point at which the roster is loaded and its size is
  // known. An index nobody has is the zero character in the sim, which is a
  // fighter that stands still and never says why.
  const roster = numCharacters()
  chars = [chars[0] % roster, chars[1] % roster]
  reset(training, ai, chars)

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

  const strip = new Graphics()
  app.stage.addChild(strip)

  const stripKey = new Text({
    text: '',
    style: {fill: 0x8a94a6, fontFamily: 'monospace', fontSize: 10},
  })
  stripKey.position.set(STRIP_X, STRIP_Y + STRIP_H + 2)
  app.stage.addChild(stripKey)

  const hud = new Text({
    text: '',
    style: {fill: 0x8a94a6, fontFamily: 'monospace', fontSize: 12},
  })
  hud.position.set(8, 66)
  app.stage.addChild(hud)

  // The lab overlay. Training only, because both readouts are there to be
  // studied between attempts and a match is not the place to read a column of
  // numbers — and because the mode is the one that lets you repeat the same
  // situation until the number means something.
  const lab = training ? createLab() : null
  const dummy = training ? createDummy() : null
  const labText = (x: number, y: number, anchor: number, size: number) => {
    const t = new Text({
      text: '',
      style: {fill: 0x8a94a6, fontFamily: 'monospace', fontSize: size},
    })
    t.position.set(x, y)
    t.anchor.set(anchor, 0)
    app.stage.addChild(t)
    return t
  }
  // One column per seat, at the edges the fighters spend least time against.
  const inputCols = lab ? [labText(8, 190, 0, 11), labText(VIEW_W - 8, 190, 1, 11)] : null
  // Over the column for the seat it drives, so what the dummy is doing and what
  // it is pressing read as one thing.
  const dummyText = dummy ? labText(VIEW_W - 8, 176, 1, 11) : null
  // Named for the frame advantage on screen, not the netplay one: net.advantage
  // is how far ahead of the other machine this one is running.
  const advText = lab ? labText(VIEW_W / 2, 38, 0.5, 16) : null

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
  // The match this session was recorded as, and whether its result has gone up.
  // **Once, on the first frame the match is over**: the phase stays MATCH_END
  // for as long as the screen shows it, and an upload per frame would be the
  // same result a hundred times.
  let matchID = 0
  let reported = false
  /** What the server made of the upload, for the HUD. */
  let uploadStatus = ''
  let ticks = 0
  let hudTicks = 0

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
  /**
   * The session so far in the replay log format. Under netplay the driver's log
   * is the authority: it holds the inputs that were *confirmed*, which is what
   * the match actually simulated once every rollback had been applied. A log
   * kept out here would record the predictions instead, and replay a match
   * nobody played.
   *
   * The setup goes in the header, or the inputs replay as a different match. A
   * netplay session is always a plain match whatever the URL said — connect()
   * resets to one before the first frame.
   */
  const logBytes = (): ArrayBuffer => {
    const src = net ? net.log : recorded
    const setup: Setup = net ? {training: false, ai: 0, chars} : {training, ai, chars}
    return encodeLog(setup, dataVersion(), src)
  }

  const pump = createEvents(eventsAt)
  const sparks: {x: number; y: number; life: number; color: number}[] = []

  app.ticker.add((ticker) => {
    frameCost.push(ticker.deltaMS)
    // A frame that took longer than one and a half steps is one the display
    // did not get. Counted rather than averaged: under continuous rollback the
    // question is whether any frame was missed, not what the mean was.
    if (ticker.deltaMS > STEP_MS * 1.5) longFrames++

    for (let i = clock.tick(ticker.deltaMS); i > 0; i--) {
      const [keys, p2] = input.poll()
      // The bot is a device: it stands in for the local player's keyboard and
      // is read once per fixed step, exactly where the keyboard is read.
      const p1 = bot ? bot.poll() : keys
      // So is the dummy, in the other seat. It decides from the state as of
      // the frame just simulated, which is the same one frame late a player
      // reacting to the screen is, and it is handed seat 2's own keys — which
      // it passes through when set to manual and keeps when recording.
      const p2in = dummy ? dummy.poll(readSnapshot(), p2) : p2

      const t0 = performance.now()
      if (net) {
        // WASD is always the local player, whichever seat they are in. Which
        // half of the keyboard you use should not depend on who dialled.
        net.step(p1)
        if (++ticks % PING_EVERY === 0) net.ping()
      } else {
        advance(p1, p2in)
        // What the sim was fed, dummy included — a log that recorded the
        // keyboard instead would replay a lab session nobody played.
        recorded.push(p1, p2in)
      }
      stepCost.push(performance.now() - t0)

      // Per simulated frame, not per displayed one: a display frame that runs
      // two sim steps would otherwise lose a row of the input display and
      // could miss the frame a recovery ended on.
      lab?.step(readSnapshot(), [p1, p2in])
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

    // **The result goes up once the match is over and the frame is settled.**
    // Settled matters: MATCH_END on a predicted frame can still be rolled back,
    // and a result uploaded from a prediction is a result of a match that did
    // not happen. Only a recorded match has anywhere to send it.
    if (net && matchID && !reported && snap.phase === PHASE_MATCH_END &&
        snap.winner !== NOBODY && net.confirmed >= snap.frame) {
      reported = true
      void uploadResult(matchID, {
        winner: snap.winner + 1, // the sim counts seats from 0, the server from 1
        endFrame: snap.frame,
        chars,
        inputLog: new Uint8Array(logBytes()),
        checksums: net.checksums(),
      }).then((status) => {
        uploadStatus = status
      })
    }

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

    // Rollback made visible. Locally there is nothing to show — every frame is
    // clean by construction — so the strip appears with the connection.
    if (net) {
      drawTimeline(strip, net)
      setText(stripKey, 'clean · predicted · corrected   ▏= rollback landed, height = depth')
    } else {
      strip.clear()
      setText(stripKey, '')
    }

    // Every frame, unlike the HUD below: an input display that refreshed twice
    // a second would not show the input it exists to show.
    if (lab && inputCols && advText) {
      for (let seat = 0; seat < inputCols.length; seat++) {
        setText(inputCols[seat], lab.history(seat).join('\n'))
      }
      setText(advText, advantageText(lab.advantage()))
    }
    if (dummy && dummyText) {
      // The frame count is the whole recording UI: it is how you know the take
      // is running, and how long the one you are about to loop is.
      const n = dummy.frames()
      setText(dummyText, `DUMMY ${dummy.mode().toUpperCase()}${n ? ` ${n}f` : ''}`)
    }

    // ponytail: no interpolation. The sim and the display are both ~60 Hz, so
    // add it when the judder is actually visible, not before.
    if (hudTicks++ % HUD_EVERY === 0) {
      const top = net ? netHud(net, link, uploadStatus) : localHud(snap)
      hud.text = `${top}\n${costHud(stepCost, frameCost, longFrames)}`
    }
  })

  return {
    training,

    // A getter, not the value: connect() replaces the pair with the room's, and
    // a field captured here would still be reporting what this end picked.
    get chars() {
      return chars
    },

    restart() {
      reset(training, ai, chars)
      pump.reset()
      lab?.reset()
      sparks.length = 0
      recorded.length = 0
    },

    setDummy(mode) {
      dummy?.setMode(mode)
    },

    inputLog() {
      return new Blob([logBytes()], {type: 'application/octet-stream'})
    },

    connect(peer, seat, pair, id) {
      // Never into a training match: the mode is in the state, so two ends that
      // disagreed about it would desync on frame 0. The panel refuses the
      // connection before this, and this is the second lock on the same door.
      //
      // The characters come along, because they are state too, and **the pair
      // the room agreed on wins over the one this end picked**: the host
      // decides both, exactly as it decides the transport (D88). Two ends that
      // chose differently do not play a mismatch — they disagree on the frame-0
      // checksum and desync before anybody has pressed anything, which is a
      // failure with no symptom that points at its cause.
      //
      // The fallback is this end's own choice, for the hand-signalled path,
      // which has no room to agree over.
      chars = pair ?? chars
      // Zero for a private match by code, which is not recorded: a ladder made
      // of matches two people arranged between themselves is not a ladder.
      matchID = id ?? 0
      reported = false
      reset(false, 0, chars)
      // The match restarts at frame 0, so what has been fired restarts with it.
      pump.reset()
      sparks.length = 0
      ticks = 0
      longFrames = 0
      // Both ends of a measured run open the same URL, so the seat is the only
      // thing that can make the two input streams differ. Without this they
      // mirror each other and every prediction is right for the wrong reason.
      bot?.reseed(seat + 1)
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

    measure: () => ({
      connected: net !== null,
      frame: net ? net.frame : readSnapshot().frame,
      advantage: net?.advantage ?? 0,
      rttP50: net?.stats.rtt.percentile(50) ?? NaN,
      rttP99: net?.stats.rtt.percentile(99) ?? NaN,
      rollbacks: net?.stats.rollbacks ?? 0,
      mispredicted: net?.stats.mispredicted ?? 0,
      depthP99: net ? depthP99(net.stats.depths) : 0,
      frames: net?.stats.frames ?? 0,
      stalls: net?.stats.stalls ?? 0,
      skipped: net?.stats.skipped ?? 0,
      verified: net?.stats.verified ?? 0,
      desyncs: net?.stats.desyncs ?? 0,
      desyncFrame: net?.stats.desyncFrame ?? -1,
      dropped: net?.stats.dropped ?? 0,
      simP50: stepCost.percentile(50),
      simP99: stepCost.percentile(99),
      byDepth: costByDepth(stepCost, net),
      displayP50: frameCost.percentile(50),
      displayP99: frameCost.percentile(99),
      longFrames,
      impaired: link ? link.cfg : null,
      lost: link?.stats.dropped ?? 0,
    }),

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

/**
 * Three seconds of frames, oldest on the left, coloured by what each was
 * simulated from — and a white tick wherever a correction landed, as tall as
 * the rollback was deep.
 *
 * Runs of the same colour are merged into one rectangle. A healthy match is
 * 180 clean frames and draws about three: the visualiser measures a frame
 * budget it must not spend, and 180 rectangles a frame is a real cost in the
 * one mode where frame time is the thing being reported.
 */
function drawTimeline(g: Graphics, net: Netplay): void {
  g.clear()
  const end = net.frame - 1
  const first = end - TIMELINE + 1

  g.rect(STRIP_X, STRIP_Y - STRIP_DEPTH_H, TIMELINE * STRIP_COL, STRIP_DEPTH_H + STRIP_H).fill({
    color: 0x0e1014,
    alpha: 0.55,
  })

  let runStart = 0
  let runKind = -1

  const flush = (until: number) => {
    if (runKind < 0) return
    const x = STRIP_X + runStart * STRIP_COL
    g.rect(x, STRIP_Y, (until - runStart) * STRIP_COL, STRIP_H).fill(STRIP_COLORS[runKind])
  }

  for (let i = 0; i < TIMELINE; i++) {
    const packed = net.sample(first + i)
    const kind = packed < 0 ? -1 : packed & 3
    if (kind !== runKind) {
      flush(i)
      runStart = i
      runKind = kind
    }

    // Ticks are drawn per frame rather than per run: two corrections a frame
    // apart are two events, and merging them would report one.
    const depth = packed < 0 ? 0 : packed >> 2
    if (depth > 0) {
      const h = (depth / MAX_ROLLBACK) * STRIP_DEPTH_H
      g.rect(STRIP_X + i * STRIP_COL, STRIP_Y - h, STRIP_COL, h).fill(0xe8ecf2)
    }
  }
  flush(TIMELINE)

  // The confirmed line: everything to its right is a prediction that can still
  // be taken back, which is also the line effects are fired up to.
  const c = net.confirmed - first
  if (c >= 0 && c < TIMELINE) {
    g.rect(STRIP_X + c * STRIP_COL, STRIP_Y - 3, 1, STRIP_H + 6).fill(0xe8ecf2)
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

/**
 * One line per player. The HUD is 12px monospace on an 800-wide canvas, which
 * is about 110 characters: both players on one line runs off the right edge,
 * and what falls off is not drawn anywhere else.
 */
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
    `frame ${snap.frame}${snap.hitstop ? `  HITSTOP ${snap.hitstop}` : ''}`,
    who(0),
    who(1),
  ].join('\n')
}

/**
 * M-A as the methodology asks for it: cost **by rollback depth**, one row per
 * depth (01 Thesis/Measurement Methodology.md).
 *
 * Row 0 is the frame's own advance, which every frame pays. Row d is what the
 * replay at depth d cost *on top of it*, so a frame a correction landed on cost
 * row 0 plus row d — and a frame with an 8-frame rollback ran the sim nine
 * times and still had to fit 16.6 ms.
 *
 * They are kept apart rather than summed here because the two are timed in
 * different places: the advance happens inside the fixed step, and the replay
 * happens whenever the packet that forced it arrives, which is its own task
 * between two displayed frames. `displayP99` is where their sum shows up.
 */
function costByDepth(step: Samples, net: Netplay | null) {
  // percentile(100) is the maximum: the worst frame is what breaks a fighting
  // game, so it is reported beside the tail rather than in place of it.
  const row = (depth: number, s: Samples) => ({
    depth,
    count: s.count,
    p50: s.percentile(50),
    p99: s.percentile(99),
    max: s.percentile(100),
  })

  const rows = [row(0, step)]
  if (net) {
    for (let d = 1; d <= MAX_ROLLBACK; d++) rows.push(row(d, net.stats.replayByDepth[d]))
  }
  return rows
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
 *
 * **Grouped onto short lines, not joined into one.** All of this on a single
 * line is roughly 200 characters against the ~110 the canvas holds, so the
 * back half — the stall, skip and desync counts — was being drawn past the
 * right edge. This is the readout the M0 numbers are screenshotted from, so
 * every field has to be on screen at once.
 */
function netHud(net: Netplay, link: Link | null, upload: string): string {
  const s = net.stats
  if (s.dataMismatch) {
    return 'REFUSED: the peer has different character data.\nRebuild both ends from the same commit.'
  }
  const pct = (n: number) => ((n / Math.max(1, s.frames)) * 100).toFixed(1)
  const rtt = (p: number) => (s.rtt.count ? s.rtt.percentile(p).toFixed(1) : '—')

  return [
    // Ahead and behind read differently and both matter: the leading end is
    // the one that skips, and a skip count that never moves on a laggy link
    // means time synchronisation is not doing its job.
    `frame ${net.frame}  ahead ${net.advantage > 0 ? '+' : ''}${net.advantage}  ` +
      `rtt p50 ${rtt(50)}ms p99 ${rtt(99)}ms`,
    `rollback ${pct(s.rollbacks)}% depth p99 ${depthP99(s.depths)}  ` +
      `mispredict ${pct(s.mispredicted)}%`,
    `stalls ${s.stalls}  skips ${s.skipped}  checked ${s.verified}  ` +
      (s.desyncs ? `DESYNC at frame ${s.desyncFrame}` : 'desync 0'),
    conditions(link),
    // What the server made of the result, once there is one. Worth showing:
    // "settled" and "mismatch" are the difference between a ranked match that
    // counted and one that voided both players' evening.
    upload && `result: ${upload}`,
  ]
    .filter(Boolean)
    .join('\n')
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
