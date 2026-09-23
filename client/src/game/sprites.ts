/**
 * Sprite sheets: Aseprite's export read into per-tag texture runs.
 *
 * The tag naming rule is D113 — an attack draws from a tag named after the
 * move's id, everything else from a tag named after the sim's state. Neither
 * name is invented here, which is the point: the view looks up names the
 * simulation already had.
 *
 * Nothing in this file writes sim state and nothing it computes travels back.
 * Animation is chosen from the render snapshot alone, so it costs nothing on a
 * rollback — a frame resimulated eight times picks the same texture eight
 * times.
 */

import {Assets, Rectangle, Texture} from 'pixi.js'

import {STATE_NAMES} from '../sim/wasm'

/** Aseprite's JSON export, plus the move table the generator adds (D113). */
export interface SheetMeta {
  frames: {frame: {x: number; y: number; w: number; h: number}}[]
  meta: {
    origin: {x: number; y: number}
    frameTags: {name: string; from: number; to: number}[]
    moveTags: string[]
  }
}

export interface Sheet {
  /**
   * The sheet's origin as a Pixi anchor, so a sprite placed at the fighter's
   * simulated position lands with its feet there. Computed here rather than at
   * the call site because it is a property of the sheet, and a sheet whose
   * cells changed size would otherwise move every character half a frame.
   */
  readonly anchor: {x: number; y: number}
  /** The texture a fighter in this state is on. Never throws. */
  texture(state: number, stateFrame: number, moveIndex: number): Texture
}

const ATTACK = STATE_NAMES.indexOf('attack')

/**
 * State to tag. Two states borrow another's animation rather than owning
 * one — a pre-jump and a landing are both a crouch — which is the cheapest
 * frames in the budget (D113).
 *
 * Keyed by name rather than by index so that appending a state to
 * sim/fighter.go, which is the only way that list ever changes, fails here at
 * startup instead of quietly rendering the new state as an idle.
 */
const STATE_TAG: Record<string, string> = {
  idle: 'idle',
  walkF: 'walk_f',
  walkB: 'walk_b',
  crouch: 'crouch',
  dash: 'dash',
  backdash: 'dash_b',
  prejump: 'crouch',
  air: 'jump',
  attack: '', // the move names it, not the state
  hitstun: 'hitstun',
  blockstun: 'blockstun',
  landing: 'crouch',
  thrown: 'thrown',
  knockdown: 'knockdown',
}

/** The tag a fighter in this state draws from, or '' if there is none. */
export function tagFor(meta: SheetMeta, state: number, moveIndex: number): string {
  if (state === ATTACK) return meta.meta.moveTags[moveIndex] ?? ''
  return STATE_TAG[STATE_NAMES[state]] ?? ''
}

/**
 * Every tag the sheet is asked for but does not carry, deduplicated.
 *
 * An empty result is the only acceptable one, and it is checked at load rather
 * than on the frame that needs it: **a missing animation must fail loudly at
 * startup, not render as an invisible character mid-match.** The failure it
 * exists for is the boring one — a move added to the character JSON and the
 * sheet not regenerated.
 */
export function missingTags(meta: SheetMeta): string[] {
  const have = new Set(meta.meta.frameTags.map((t) => t.name))
  const want = new Set<string>()

  for (const name of STATE_NAMES) {
    const tag = STATE_TAG[name]
    if (tag === undefined) want.add(`<state ${name} has no tag>`)
    else if (tag) want.add(tag)
  }
  for (const tag of meta.meta.moveTags) want.add(tag)

  return [...want].filter((t) => !have.has(t)).sort()
}

/** Slices a loaded sheet into per-tag texture runs. */
export function buildSheet(meta: SheetMeta, base: Texture): Sheet {
  const missing = missingTags(meta)
  if (missing.length > 0) {
    throw new Error(`sprites: sheet is missing ${missing.length} tag(s): ${missing.join(', ')}`)
  }

  const runs = new Map<string, Texture[]>()
  for (const tag of meta.meta.frameTags) {
    const run: Texture[] = []
    for (let i = tag.from; i <= tag.to; i++) {
      const f = meta.frames[i].frame
      run.push(new Texture({source: base.source, frame: new Rectangle(f.x, f.y, f.w, f.h)}))
    }
    runs.set(tag.name, run)
  }

  const idle = runs.get('idle')!
  const cell = meta.frames[0].frame

  return {
    anchor: {x: meta.meta.origin.x / cell.w, y: meta.meta.origin.y / cell.h},
    texture(state, stateFrame, moveIndex) {
      // Startup validation means every tag resolves, so the fallbacks below
      // are for the two things it cannot cover: a move index of -1, which the
      // sim uses for "no move", and a state frame that has outrun its
      // animation. Neither is an error and neither may throw on a frame.
      const run = runs.get(tagFor(meta, state, moveIndex)) ?? idle
      return run[Math.min(Math.max(stateFrame, 0), run.length - 1)]
    },
  }
}

/** Fetches and slices one character's sheet. */
export async function loadSheet(name: string): Promise<Sheet> {
  const res = await fetch(`/sprites/${name}.json`)
  if (!res.ok) throw new Error(`sprites: /sprites/${name}.json (${res.status})`)
  const meta = (await res.json()) as SheetMeta
  return buildSheet(meta, await Assets.load(`/sprites/${name}.png`))
}
