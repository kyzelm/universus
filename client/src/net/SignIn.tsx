import {useState} from 'react'
import {account, signIn, signOut, type Account} from './account'

/**
 * Sign in or register, in the net panel where the thing that needs an account
 * is. **Nothing offline is behind it**: training, local versus and versus AI
 * never ask (D62), and this whole section is invisible until somebody wants to
 * play a stranger or a friend.
 *
 * One form for both, switched by a checkbox, because they differ by one field.
 */
export default function SignIn({onChange}: {onChange(a: Account | null): void}) {
  const [who, setWho] = useState<Account | null>(account)
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [displayName, setDisplayName] = useState('')
  const [registering, setRegistering] = useState(false)
  const [status, setStatus] = useState('')
  const [busy, setBusy] = useState(false)

  function done(a: Account | null) {
    setWho(a)
    onChange(a)
  }

  async function submit(e: React.FormEvent) {
    e.preventDefault()
    setBusy(true)
    setStatus('')
    try {
      done(await signIn(email, password, registering ? displayName : undefined))
      setPassword('')
    } catch (err) {
      // The server's own words: it says "wrong email or password" for three
      // different situations on purpose, and rewording that here would be
      // guessing which one happened.
      setStatus((err as Error).message)
    } finally {
      setBusy(false)
    }
  }

  if (who) {
    return (
      <div className="row">
        <span className="keys">signed in as {who.displayName}</span>
        <button
          type="button"
          onClick={() => {
            signOut()
            done(null)
          }}
        >
          sign out
        </button>
      </div>
    )
  }

  return (
    <form className="signin" onSubmit={(e) => void submit(e)}>
      <div className="row">
        <input
          type="email"
          placeholder="email"
          autoComplete="email"
          value={email}
          onChange={(e) => setEmail(e.target.value)}
        />
        <input
          type="password"
          placeholder="password"
          // The browser's password manager fills a different field for a new
          // account than for an existing one, and telling it which is the
          // difference between it helping and it fighting.
          autoComplete={registering ? 'new-password' : 'current-password'}
          value={password}
          onChange={(e) => setPassword(e.target.value)}
        />
        {registering && (
          <input
            placeholder="display name"
            value={displayName}
            onChange={(e) => setDisplayName(e.target.value)}
          />
        )}
        <button type="submit" disabled={busy || !email || !password}>
          {registering ? 'register' : 'sign in'}
        </button>
      </div>

      <p className="keys">
        <label>
          <input
            type="checkbox"
            checked={registering}
            onChange={(e) => setRegistering(e.target.checked)}
          />{' '}
          new account
        </label>
        {status && ` — ${status}`}
      </p>
    </form>
  )
}
