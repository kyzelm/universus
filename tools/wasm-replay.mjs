// Runs an input log through the WASM build of the sim and prints
// "<frame> <checksum>" per line — byte-identical output to `replay run`, which
// is what tools/determinism.sh compares.
//
// With a third argument it instead prints the full state at that frame, one
// field per line — the WASM half of the gate's state diff.
//
// Usage: node tools/wasm-replay.mjs <main.wasm> <log.inputs> [dumpFrame]
import {readFileSync} from 'node:fs'
import {loadSim} from './wasm-load.mjs'

const [wasmPath, logPath, dumpFrame] = process.argv.slice(2)
if (!wasmPath || !logPath) {
  console.error('usage: node tools/wasm-replay.mjs <main.wasm> <log.inputs>')
  process.exit(1)
}

const sim = await loadSim(wasmPath)

const log = readFileSync(logPath)
if (log.length === 0 || log.length % 4 !== 0) {
  console.error(`${logPath}: ${log.length} bytes is not a whole number of 4-byte frames`)
  process.exit(1)
}

if (dumpFrame !== undefined) {
  const upTo = Number(dumpFrame)
  if (!Number.isInteger(upTo) || upTo < 0 || upTo * 4 > log.length) {
    console.error(`dump frame ${dumpFrame} is not inside a ${log.length / 4}-frame log`)
    process.exit(1)
  }
  for (let f = 0; f < upTo; f++) {
    sim.advance(log.readUInt16LE(f * 4), log.readUInt16LE(f * 4 + 2))
  }
  process.stdout.write(sim.dump())
  sim.quit()
  process.exit(0)
}

const hex = (n) => (n >>> 0).toString(16).padStart(8, '0')

// One string, one write: 10 000 stdout calls dominate everything else.
const out = [`0 ${hex(sim.checksum())}`]
for (let f = 0; f * 4 < log.length; f++) {
  sim.advance(log.readUInt16LE(f * 4), log.readUInt16LE(f * 4 + 2))
  out.push(`${f + 1} ${hex(sim.checksum())}`)
}
process.stdout.write(out.join('\n') + '\n')

sim.quit()
