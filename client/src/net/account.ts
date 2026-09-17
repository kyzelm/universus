/**
 * The account, as the client needs it (02 Architecture/Backend Services.md).
 *
 * **Offline play never touches any of this.** Training, local versus and versus
 * AI require no account at all (03 Game Design/Game Modes.md, D62), and a
 * server running without a database has no accounts to offer — both are
 * ordinary states rather than errors, which is why every call here reports a
 * failure the menu can print instead of throwing.
 *
 * The token lives in localStorage. That is a real trade and worth naming: it is
 * readable by script, so an XSS on this origin is a stolen session. The
 * alternative is an httpOnly cookie, which cannot be read by the page — and the
 * WebSocket endpoints need the token *in a frame*, because a browser cannot set
 * headers on a socket and a credential in the URL is written to every log it
 * passes. Cookies would also bring CSRF back for the sake of a page with no
 * cross-origin forms. At this scale the cookie's advantage is theoretical and
 * its cost is a second mechanism.
 */

const KEY = 'universus.session'

export interface Account {
  id: number
  email: string
  displayName: string
}

interface Stored {
  token: string
  user: Account
}

function read(): Stored | null {
  try {
    const raw = localStorage.getItem(KEY)
    return raw ? (JSON.parse(raw) as Stored) : null
  } catch {
    // Private windows, cleared site data, storage disabled entirely. Signed out
    // is the honest reading of all three.
    return null
  }
}

/** The bearer token, or '' — which is exactly what an anonymous client sends. */
export function token(): string {
  return read()?.token ?? ''
}

export function account(): Account | null {
  return read()?.user ?? null
}

export function signOut(): void {
  try {
    localStorage.removeItem(KEY)
  } catch {
    // Nothing to remove if it could never be written.
  }
}

/**
 * Registers or signs in, and remembers the session. The two differ by one field
 * and one path, so they are one function rather than two that drift.
 */
export async function signIn(
  email: string,
  password: string,
  displayName?: string,
): Promise<Account> {
  const path = displayName === undefined ? '/api/auth/login' : '/api/auth/register'

  let res: Response
  try {
    res = await fetch(path, {
      method: 'POST',
      headers: {'Content-Type': 'application/json'},
      body: JSON.stringify(displayName === undefined ? {email, password} : {email, password, displayName}),
    })
  } catch {
    throw new Error('no server to sign in to')
  }

  // A server with no database does not register these routes at all, which
  // arrives here as a 404 and means something specific worth saying.
  if (res.status === 404) {
    throw new Error('this server has no accounts, so online play is not available')
  }

  const body = (await res.json().catch(() => ({}))) as {
    token?: string
    user?: Account
    error?: string
  }
  if (!res.ok || !body.token || !body.user) {
    throw new Error(body.error ?? `sign-in failed (${res.status})`)
  }

  try {
    localStorage.setItem(KEY, JSON.stringify({token: body.token, user: body.user}))
  } catch {
    // The session still works for this page; it just will not outlive it.
  }
  return body.user
}
