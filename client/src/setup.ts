import {AI_TIERS} from './sim/wasm'

/**
 * The modes a run can be started in (03 Game Design/Game Modes.md). Casual and
 * ranked are one entry — what separates them is matchmaking and verification,
 * which is backend work, and until there is a backend they are the same socket.
 */
export const MODES = ['local', 'ai', 'training', 'online'] as const
export type Mode = (typeof MODES)[number]

/**
 * Everything a run is started from — the same three things a replay log's
 * header carries, because they are the same three things the input stream
 * cannot recover (tools/replay/main.go).
 */
export interface RunSetup {
  mode: Mode
  /** Difficulty tier, 1..3, and 0 in every mode but `ai`. */
  ai: number
  chars: [number, number]
}

/**
 * ?ai, or ?ai=easy|normal|hard. Vs-AI is the mode that demos with no network
 * and no second machine (03 Game Design/AI Opponent.md), so it is reachable
 * from the URL like everything else a run is set up with.
 */
export function aiFromURL(params: URLSearchParams): number {
  if (!params.has('ai')) return 0
  const tier = AI_TIERS.indexOf((params.get('ai') || 'normal') as (typeof AI_TIERS)[number])
  return tier > 0 ? tier : AI_TIERS.indexOf('normal')
}

/**
 * ?p1=0&p2=1 picks the roster entries, defaulting to the first for both.
 *
 * The character select builds the same pair; this is how a measurement run, a
 * CI run and a shared link pass one. **Both ends of a netplay session must pass
 * the same pair**: the characters are in `GameState`, so two clients that
 * disagreed would desync on frame 0 rather than play a mismatch.
 */
export function charsFromURL(params: URLSearchParams): [number, number] {
  const pick = (key: string) => {
    const n = Number(params.get(key))
    return Number.isInteger(n) && n >= 0 ? n : 0
  }
  return [pick('p1'), pick('p2')]
}

/**
 * The parameters that name part of a run. **Including the ones a person never
 * types**: `?bot` and `?bench` are measurement harnesses, `?room` is a link
 * somebody was sent, and all of them have to land in the game rather than on a
 * title screen waiting to be clicked.
 */
const RUN_PARAMS = ['training', 'ai', 'room', 'bot', 'bench', 'p1', 'p2']

/**
 * The setup a URL describes, or null when it describes no run at all — which is
 * somebody arriving, and they get the menu.
 */
export function setupFromURL(search = location.search): RunSetup | null {
  const params = new URLSearchParams(search)
  if (!RUN_PARAMS.some((k) => params.has(k))) return null

  const ai = aiFromURL(params)
  const mode: Mode = params.has('training')
    ? 'training'
    : ai > 0
      ? 'ai'
      : params.has('room')
        ? 'online'
        : 'local'

  return {mode, ai, chars: charsFromURL(params)}
}
