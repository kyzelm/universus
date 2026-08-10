// Runs an input log through the WASM build of the sim and prints
// "<frame> <checksum>" per line — byte-identical output to `replay run`, which
// is what tools/determinism.sh compares.
//
// Usage: node tools/wasm-replay.mjs <main.wasm> <log.inputs>
import {readFileSync} from 'node:fs'
import {createRequire} from 'node:module'
import {execFileSync} from 'node:child_process'

const [wasmPath, logPath] = process.argv.slice(2)
if (!wasmPath || !logPath) {
  console.error('usage: node tools/wasm-replay.mjs <main.wasm> <log.inputs>')
  process.exit(1)
}

// wasm_exec.js is a plain script that assigns globalThis.Go; require runs it
// for its side effect. GOROOT owns the copy that matches this toolchain — the
// one in client/public/ is a build artifact and can be stale.
const goroot = process.env.GOROOT || execFileSync('go', ['env', 'GOROOT'], {encoding: 'utf8'}).trim()
createRequire(import.meta.url)(`${goroot}/lib/wasm/wasm_exec.js`)

const go = new Go()
const {instance} = await WebAssembly.instantiate(readFileSync(wasmPath), go.importObject)

// Go's main blocks on quit(), so this promise resolves at the end, not here.
// The API is on globalThis by the time run() returns.
void go.run(instance)

const log = readFileSync(logPath)
if (log.length === 0 || log.length % 4 !== 0) {
  console.error(`${logPath}: ${log.length} bytes is not a whole number of 4-byte frames`)
  process.exit(1)
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
