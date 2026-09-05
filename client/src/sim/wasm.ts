declare class Go {
  importObject: WebAssembly.Imports

  run(instance: WebAssembly.Instance): Promise<void>
}

declare global {
  let sim: {
    advance(p1: number, p2: number): void
    rewind(frame: number): boolean
    reset(): void
    checksum(): number
    dataVersion(): number
    snapshotPtr(): number
    snapshotLen(): number
    noop(): void
    quit(): void
  }
}

/** 16.16 fixed-point scale. The view divides by it; the sim never does. */
const ONE = 65536

/**
 * Snapshot layout — the contract with sim/snapshot.go, which cannot check it
 * from its side. These offsets and that file change together.
 */
const MAX_BOXES = 4
const MAX_PROJECTILES = 4
const BOX = 4 * 4
const HIT_OFF = 68 + MAX_BOXES * BOX
const PLAYER_SIZE = 12 * 4 + BOX + 2 * (4 + MAX_BOXES * BOX)
const HEADER = 9 * 4
const PROJ_OFF = HEADER + 2 * PLAYER_SIZE

/** Mirrors the state constants in sim/fighter.go, for the debug readout. */
export const STATE_NAMES = [
  'idle',
  'walkF',
  'walkB',
  'crouch',
  'dash',
  'backdash',
  'prejump',
  'air',
  'attack',
  'hitstun',
  'blockstun',
  'landing',
  'thrown',
]

/**
 * Round phases, mirroring sim/round.go. Only PHASE_FIGHT runs the game; the
 * rest freeze both fighters and run their own clock, which is why the view can
 * key its announcement off the phase alone.
 */
export const PHASE_FIGHT = 0
export const PHASE_KO = 1
export const PHASE_ROUND_END = 2
export const PHASE_INTRO = 3
export const PHASE_MATCH_END = 4

/** No winner: a draw round, or a match that is still being played. */
export const NOBODY = -1

/**
 * Resource maxima, mirroring sim/resource.go: a bar is a thousand units, and
 * the gauges are 6 and 3 bars. The HUD needs them to draw a fraction; the sim
 * is the only thing that may change them.
 */
export const DRIVE_BARS = 6
export const SUPER_BARS = 3
export const BAR_UNITS = 1000

/** Counter-hit classes, mirroring sim/damage.go. Index is the snapshot value. */
export const COUNTER_NAMES = ['', 'COUNTER', 'PUNISH COUNTER']

export interface Box {
  x: number
  y: number
  w: number
  h: number
}

export interface PlayerSnapshot {
  x: number
  y: number
  facing: number
  moveIndex: number
  state: number
  stateFrame: number
  health: number
  drive: number
  super: number
  /** 1 while the Drive gauge is refilling from empty. */
  burnout: number
  /** Hits taken without recovering; 0 when not in a combo. */
  combo: number
  /** Class of the most recent hit taken — index into COUNTER_NAMES. */
  counter: number
  pushbox: Box
  hurtboxes: Box[]
  hitboxes: Box[]
}

export interface Snapshot {
  frame: number
  camX: number
  hitstop: number
  /** Round phase — one of the PHASE_* constants. */
  phase: number
  /** The round clock, in frames. The view is what turns it into seconds. */
  timer: number
  /** Rounds won, per seat. */
  wins: number[]
  /** Who took the round just decided, or NOBODY for a draw. */
  roundWinner: number
  /** Match winner once the phase is PHASE_MATCH_END, NOBODY until then. */
  winner: number
  players: PlayerSnapshot[]
  /**
   * Live projectiles, packed from the front — the sim's slot numbers do not
   * cross, because nothing over here needs to name one.
   */
  projectiles: Box[]
}

let mem: WebAssembly.Memory
let view: DataView
let loading: Promise<void> | undefined

/** Fetches and starts the sim. Idempotent. */
export function loadSim(): Promise<void> {
  return (loading ??= fetchAndInit())
}

async function fetchAndInit(): Promise<void> {
  const res = await fetch('/main.wasm')
  const type = res.headers.get('content-type')
  if (!res.ok || type !== 'application/wasm') {
    throw new Error(`sim: /main.wasm not served as wasm (${res.status}, ${type})`)
  }
  await initSim(res)
}

/**
 * Starts the sim from an already-obtained module. Exists so tests can run the
 * real module off disk without a server; the browser goes through loadSim.
 */
export async function initSim(src: Response | BufferSource): Promise<void> {
  const go = new Go()
  const {instance} =
    src instanceof Response
      ? await WebAssembly.instantiateStreaming(src, go.importObject)
      : await WebAssembly.instantiate(src, go.importObject)

  // go.run resolves only when the sim calls quit, so it is deliberately not awaited.
  void go.run(instance)

  mem = instance.exports.mem as WebAssembly.Memory
  view = newView()
}

function newView(): DataView {
  return new DataView(mem.buffer, sim.snapshotPtr(), sim.snapshotLen())
}

/**
 * The snapshot buffer. Go's memory growth detaches every ArrayBuffer, so the
 * view is re-created when that happens rather than created fresh each frame.
 */
function snapshot(): DataView {
  if (view.buffer !== mem.buffer) view = newView()
  return view
}

export function advance(p1: number, p2: number): void {
  sim.advance(p1, p2)
}

/**
 * Restores the state at the start of `frame`; the caller replays with advance.
 * False means the frame is outside the rollback window and the correction
 * arrived too late to apply.
 */
export function rewind(frame: number): boolean {
  return sim.rewind(frame)
}

export function reset(): void {
  sim.reset()
}

/** FNV-1a over the packed state. The number both machines must agree on. */
export function checksum(): number {
  return sim.checksum()
}

/**
 * Hash of the embedded character data. Compared at handshake: two clients with
 * different frame data desync on the first exchange and there is no way to work
 * out why from the checksums alone.
 */
export function dataVersion(): number {
  return sim.dataVersion()
}

function readBox(v: DataView, o: number): Box {
  return {
    x: v.getInt32(o, true) / ONE,
    y: v.getInt32(o + 4, true) / ONE,
    w: v.getInt32(o + 8, true) / ONE,
    h: v.getInt32(o + 12, true) / ONE,
  }
}

function readBoxList(v: DataView, countOffset: number, cap = MAX_BOXES): Box[] {
  const n = v.getUint32(countOffset, true)
  const out: Box[] = []
  for (let i = 0; i < n && i < cap; i++) {
    out.push(readBox(v, countOffset + 4 + i * BOX))
  }
  return out
}

// ponytail: allocates a small object graph per frame. 60/s is nothing; read the
// DataView directly if a profile ever says otherwise.
export function readSnapshot(): Snapshot {
  const v = snapshot()
  return {
    frame: v.getUint32(0, true),
    camX: v.getInt32(4, true) / ONE,
    hitstop: v.getInt32(8, true),
    phase: v.getInt32(12, true),
    timer: v.getInt32(16, true),
    wins: [v.getInt32(20, true), v.getInt32(24, true)],
    roundWinner: v.getInt32(28, true),
    winner: v.getInt32(32, true),
    players: [0, 1].map((i) => {
      const o = HEADER + i * PLAYER_SIZE
      return {
        x: v.getInt32(o, true) / ONE,
        y: v.getInt32(o + 4, true) / ONE,
        facing: v.getInt32(o + 8, true),
        moveIndex: v.getInt32(o + 12, true),
        state: v.getInt32(o + 16, true),
        stateFrame: v.getInt32(o + 20, true),
        health: v.getInt32(o + 24, true),
        drive: v.getInt32(o + 28, true),
        super: v.getInt32(o + 32, true),
        burnout: v.getInt32(o + 36, true),
        combo: v.getInt32(o + 40, true),
        counter: v.getInt32(o + 44, true),
        pushbox: readBox(v, o + 48),
        hurtboxes: readBoxList(v, o + 64),
        hitboxes: readBoxList(v, o + HIT_OFF),
      }
    }),
    projectiles: readBoxList(v, PROJ_OFF, MAX_PROJECTILES),
  }
}
