# Universus — agent brief (code repo)

Browser 2D fighting game, BSc engineering thesis. **This is the implementation repo.** The design
vault is the source of truth for *what* to build and *why*:

```
/home/kyzelm/Obsidian/Universus/
  Universus.md                  home map, locked stack
  00 Meta/Decision Log.md       D1–D65, every locked decision + reason
  06 Roadmap/Task Board.md      concrete tasks, M0/M1 broken down
  06 Roadmap/Build Roadmap.md   milestones, M0 pass criteria
  02 Architecture/*.md          sim, fixed-point, WASM boundary, rollback, transport, backend
  03 Game Design/*.md           combat, input, character data, resources, rounds, AI
  05 Tooling/Testing and CI.md  the determinism gate
```

**Before proposing an alternative, read the Decision Log.** Most alternatives (TS sim, 32.32
fixed-point, JSON over the WASM boundary, TURN, Redis, microservices, skeletal animation, story
mode, ML AI) were already evaluated and rejected with a reason. Reversing one means updating the
vault note that links to it.

**Standing bias: the thesis outweighs the artifact.** When trading scope, cut content and roster;
never cut instrumentation, testing, or measurement — those are the thesis.

## Locked stack

Deterministic fixed-point sim in **Go → WASM** (same source compiled native for the server) ·
**PixiJS + TypeScript** view layer (read-only) · **rollback netcode**, GGPO params · **WebRTC
DataChannel** P2P with **WebSocket relay** fallback · **Go** backend · **Postgres**.

Go 1.26, module `universus`. Client: Vite + TypeScript + React (shell/menus only) + pnpm + oxlint.
PixiJS is **not installed yet** — add it when the canvas work starts.

## Non-negotiable invariants

- **No floats in the sim.** Fixed-point 16.16 in `int32` only. CI greps `./sim/` for
  `float32|float64|"math"` and fails.
- **No gameplay state outside `GameState`.** If it is not in the struct, it does not roll back, and
  it is a bug. Includes: camera, RNG, input history, AI state, timers, round transitions.
- **The view never writes sim state.** It reads a `RenderSnapshot` and draws. It may interpolate
  visually; it may never feed anything back.
- **No hardcoded gameplay values.** Frame data, damage, boxes, speeds, costs → character/balance
  JSON, `go:embed`ed so the binary hash covers the balance data.
- **The sim is single-threaded, has no clock, and does no I/O.** Frame number is the only clock.
- **No map iteration, no pointer comparison, no unseeded randomness, no goroutines** in sim code.
- **Any per-user setting (negative edge, keybinds) resolves client-side before the input bitfield is
  built.** Nothing the sim reads may depend on a local setting.

## Repo layout

```
sim/        deterministic core, zero dependencies. sim/wasm/ = WASM entrypoint
server/     Go backend: auth, matchmaking, signaling, relay, verification. imports sim natively
client/     TypeScript, Vite, PixiJS view + net layer
tools/      tools/replay = headless replay/checksum harness; frame data editor later
data/       character + balance JSON
testdata/   input log corpus — every desync bug ever found lands here as a regression log
```

Monorepo, one Go module, one version. Currently: dirs exist, Go files are empty, `client/` is still
the default Vite+React scaffold.

## Core formats — do not drift from these

```go
type Fix int32                                            // 16.16
const FracBits = 16; const One = Fix(1 << FracBits)
func (a Fix) Mul(b Fix) Fix { return Fix((int64(a) * int64(b)) >> FracBits) }
func (a Fix) Div(b Fix) Fix { return Fix((int64(a) << FracBits) / int64(b)) }
```

`GameState`: one flat, fixed-size struct. No pointers, no slices, no maps. Explicit zeroed padding.
Target < 2 KB, so save/load are memcpys and checksum is a hash over a byte range.

Checksum: FNV-1a over the packed state, every frame.

Input: one `uint16` per player per frame.
`bit0 up, 1 down, 2 left, 3 right, 4 LP, 5 MP, 6 HP, 8 LK, 9 MK, 10 HK` (7, 11 reserved).
Same bytes go to the sim, the network, the replay, and the server.

Net packet: `[uint8 type][uint32 startFrame][uint8 count][uint16 inputs[count]]`, ~20 bytes.
Types: 1 inputs, 2 checksum, 3 ping, 4 control. **Every packet carries the last ~8 frames of
inputs** — that redundancy replaces retransmission entirely. Unreliable, unordered channel
(`ordered: false, maxRetransmits: 0`).

WASM boundary: packed binary `RenderSnapshot` in linear memory, one `DataView` created once and
reused. `GameState` never crosses. Re-create the view if `wasmMemory.buffer.byteLength` changes —
Go's memory growth detaches every `ArrayBuffer`.

Rollback: 2 frames input delay, max 8 frames, prediction = repeat last input, checksum exchange
every 30 frames, ring buffer of 12 snapshots allocated once.

## Update order is part of the spec

Fixed, documented, never casually reordered:

1. resolve inputs (SOCD → buffer → motion recognition)
2. advance state machines
3. movement and gravity
4. pushboxes and stage bounds
5. projectiles
6. hit detection — **P1's hitboxes vs P2's hurtboxes first, then the reverse** (trades must resolve
   identically on both machines)
7. hit resolution: damage, scaling, hitstun, meter
8. timers, resources, round state
9. increment frame, compute checksum

Damage order, equally fixed, all integer, all divisions after multiplications:
`base → ×starterScale/100 → ×comboScale[n]/100 → max(minDamage) → ×counterHit/100`.

## Rollback traps

- Sounds, particles, screen shake, hit flashes: the sim sets **event flags in state**; the view fires
  effects only for flags newly present on *confirmed* frames. The classic bug is one sound playing
  eight times during a rollback. Build the mechanism in M2 even with no sounds attached.
- A frame with an 8-frame rollback runs the sim 9 times and must still fit 16.6 ms.
- Motion recognition scans **backward**; **DP takes priority over QCF** or every DP is a fireball.
  Test this case explicitly.
- SOCD resolves to neutral (left+right), up wins (up+down), **before** packing.

## Testing — the determinism gate is the most important test in the project

```
load input log → run native → run WASM (Node) → compare per-frame checksums
              → FAIL on first divergence, report frame number + state diff
```

CI pipeline, one GitHub Actions workflow, minutes not hours:
`go vet` + float-ban grep → `go test ./...` → build native + WASM → determinism differential over
the whole corpus → rollback correctness test → performance regression → client bundle.

Rollback correctness test: run 100 frames, save checksum; rerun the same inputs forcing rollbacks of
depth 1–8 at 20 seeded frames; final checksum must be identical. Fails immediately if any state
lives outside `GameState`.

Unit tests where correctness is a formula: fixed-point ops, damage scaling order, every motion, every
SOCD combination, box overlap edges, LP tier boundaries.

Instrument from day one: rollback frequency, depth distribution, frame time by depth, misprediction
rate, stall frequency. Report distributions and 99th percentiles, never bare means. These are the
results chapter and they are painful to retrofit.

## Current milestone: M0 — netcode spike (2 weeks, throwaway code)

**Question: does the architecture hold?** Not the game. Delete it afterwards without regret.

Week 1: Go→WASM hello-world (budget a full day, the toolchain fights back) · PixiJS one rectangle at
60 fps fixed timestep · `Fix` + tests · minimal `GameState` · `advance()`, gravity, walls · packed
snapshot read via `DataView` · keyboard → `uint16`.

Week 2: `saveState`/`loadState`/`checksum` · rollback rewind+replay · native headless replay harness
· native-vs-WASM differential over 10 000 frames · WebRTC between two tabs then two machines (manual
signaling is fine) · 8-frame input redundancy.

Pass criteria — **measure, do not estimate**:

| Target | Bar |
|---|---|
| Sim step in WASM | < 0.5 ms |
| 8-frame rollback | < 4 ms |
| Native vs WASM checksums | identical over 10 000 frames |
| P2P between two machines | connects, RTT measured |
| Frame rate under continuous rollback | sustained 60 fps |

**If a bar fails, stop and re-plan.** Escape hatches in order: reduce max rollback to 4–5 · shrink
state · try TinyGo · fall back to delay-based netcode (which makes the thesis comparative). Write the
numbers down — they are the first data in the results chapter.

Then: M1 core sim + local 2P (4w) → M2 combat + online (5w) → M3 modes + backend (5w) → M4 art
(parallel from M1) → M5 measurement + thesis. Placeholder rectangles throughout; art never blocks
engineering.

## When working here

- A task is done when it is **verified**, not when it is written.
- Record an input log for every playtest and every fixed desync; commit it to `testdata/`.
- New locked decision → append to the vault's Decision Log with the next D-number **and the reason**.
- Migrations are numbered `.sql` files applied by the Go binary at startup. No framework.
- Deliberately absent, do not add: Redis, message broker, microservices, k8s, migration framework,
  physics engine, ORM, state-management library. Expected concurrent users is single digits.
