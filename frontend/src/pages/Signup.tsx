import { useEffect, useState } from 'react'
import { Link, Navigate } from 'react-router-dom'
import { UserKey } from 'lucide-react'
import { useAuth } from '../authContext'
import { errorMessage, passkeyRegisterBegin, passkeyRegisterFinish } from '../api'
import { createPasskey, browserSupportsWebAuthn } from '../lib/webauthn'
import { goToNext, safeNextPath, nextQuery } from '../lib/authRedirect'
import { usePageTitle } from '../hooks/usePageTitle'
import { ErrorAlert } from '../components/ui'
import SSOButtons from '../components/SSOButtons'
import Logo from '../components/Logo'
import { siteURL } from '../lib/faq'
import { rawTextProps } from '../lib/inputProps'

// Signup is the /signup page. It keeps `next` through every flow, so e.g. an
// invitee lands back on /invite/:token once the account exists.
export default function Signup() {
  usePageTitle('Create account')
  const { user, loading } = useAuth()
  const next = new URLSearchParams(window.location.search).get('next')
  const alreadyAuthed = !loading && !!user
  // Invites only apply to new accounts, so signing in can't accept one.
  const isInvite = !!next && next.startsWith('/invite/')

  const [passkeySupported] = useState(() => browserSupportsWebAuthn())
  const [passkeyBusy, setPasskeyBusy] = useState(false)
  const [passkeyError, setPasskeyError] = useState<string | null>(null)
  const [username, setUsername] = useState('')

  // `next` may be a backend-only route, which needs a full navigation.
  const nextPath = safeNextPath(next)
  useEffect(() => {
    if (alreadyAuthed && nextPath) {
      window.location.replace(nextPath)
    }
  }, [alreadyAuthed, nextPath])

  if (alreadyAuthed) {
    if (nextPath) {
      return null
    }
    return <Navigate to="/" replace />
  }

  const handlePasskeyRegister = async (e: React.FormEvent) => {
    e.preventDefault()
    setPasskeyError(null)
    setPasskeyBusy(true)
    try {
      const options = await passkeyRegisterBegin(username, next ?? undefined)
      const response = await createPasskey(options)
      await passkeyRegisterFinish(response)
      goToNext(next)
    } catch (err) {
      setPasskeyError(errorMessage(err))
      setPasskeyBusy(false)
    }
  }

  return (
    <div className="hero min-h-screen bg-base-200">
      <div className="hero-content w-full text-center">
        <div className="w-full max-w-md">
          <h1 className="text-2xl font-bold mb-2">
            <a href={siteURL()} className="inline-flex items-center gap-2.5">
              <Logo className="size-7" />
              Ownscale
            </a>
          </h1>
          <p className="mb-6 text-base-content/70">Create an account to continue.</p>

          <ErrorAlert error={passkeyError} />

          <div className="flex flex-col gap-2">
            {passkeySupported ? (
              <form
                className="flex flex-col gap-2 text-left"
                onSubmit={(e) => void handlePasskeyRegister(e)}
              >
                <p className="text-sm text-base-content/70">
                  Passkeys are recommended: they don't share any personal information with Ownscale.
                </p>
                <label className="fieldset-label" htmlFor="passkey-username">
                  Username
                </label>
                <input
                  {...rawTextProps}
                  id="passkey-username"
                  type="text"
                  required
                  autoComplete="username"
                  pattern="[A-Za-z][A-Za-z0-9._\-]{1,31}"
                  title="2–32 characters, starting with a letter; letters, digits, '.', '-' and '_' only"
                  className="input input-bordered w-full"
                  placeholder="Choose any username"
                  value={username}
                  onChange={(e) => setUsername(e.target.value)}
                />
                <button type="submit" className="btn btn-primary" disabled={passkeyBusy}>
                  <UserKey className="size-4" aria-hidden="true" />
                  Continue with passkey
                </button>
              </form>
            ) : (
              <p className="text-sm text-base-content/70 text-left">
                This browser doesn't support passkeys; sign up with one of the providers below.
              </p>
            )}
            <div className="divider text-sm text-base-content/60">or</div>
            <SSOButtons verb="Sign up" next={next} />
          </div>

          {!isInvite && (
            <p className="mt-6 text-sm">
              Already have an account?{' '}
              <Link to={`/login${nextQuery(next)}`} className="link link-primary">
                Sign in
              </Link>
            </p>
          )}
        </div>
      </div>
    </div>
  )
}
