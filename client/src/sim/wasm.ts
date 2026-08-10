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
    snapshotPtr(): number
    snapshotLen(): number
    noop(): void
    quit(): void
  }
}

/** 16.16 fixed-point scale. The view divides by it; the sim never does. */
const ONE = 65536

export interface Snapshot {
  frame: number
  players: {x: number; y: number}[]
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

// ponytail: allocates a small object per frame. 60/s is nothing; read the
// DataView directly if a profile ever says otherwise.
export function readSnapshot(): Snapshot {
  const v = snapshot()
  return {
    frame: v.getUint32(0, true),
    players: [0, 1].map((i) => ({
      x: v.getInt32(4 + i * 8, true) / ONE,
      y: v.getInt32(8 + i * 8, true) / ONE,
    })),
  }
}
