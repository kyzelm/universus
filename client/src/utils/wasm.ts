declare class Go {
  importObject: WebAssembly.Imports

  run(instance: WebAssembly.Instance): Promise<void>
}

declare global {
  let sim: {
    add(a: number, b: number): number
    snapshotPtr(): number
    snapshotLen(): number
    quit(): void
  }
}

let mem: WebAssembly.Memory
let view: DataView
let loading: Promise<void> | undefined

export function loadSim(): Promise<void> {
  return (loading ??= start())
}

async function start(): Promise<void> {
  const res = await fetch('/main.wasm')
  const type = res.headers.get('content-type')
  if (!res.ok || type !== 'application/wasm') {
    throw new Error(`sim: /main.wasm not served as wasm (${res.status}, ${type})`)
  }

  const go = new Go()
  const {instance} = await WebAssembly.instantiateStreaming(res, go.importObject)
  void go.run(instance)

  mem = instance.exports.mem as WebAssembly.Memory
  view = new DataView(mem.buffer, sim.snapshotPtr(), sim.snapshotLen())
}

export function snapshot(): DataView {
  if (view.buffer !== mem.buffer) {
    view = new DataView(mem.buffer, sim.snapshotPtr(), sim.snapshotLen())
  }
  return view
}
