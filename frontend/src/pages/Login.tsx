import { useEffect, useState } from 'react'
import { Link, Navigate } from 'react-router-dom'
import { UserKey } from 'lucide-react'
import { useAuth } from '../authContext'
import { errorMessage, passkeyLoginBegin, passkeyLoginFinish } from '../api'
import { getPasskey, browserSupportsWebAuthn } from '../lib/webauthn'
import { goToNext, safeNextPath, nextQuery } from '../lib/authRedirect'
import { usePageTitle } from '../hooks/usePageTitle'
import { ErrorAlert } from '../components/ui'
import SSOButtons from '../components/SSOButtons'
import Logo from '../components/Logo'
import { siteURL } from '../lib/faq'

// isAuthorizeCallback reports whether next finishes another organization's
// headscale SSO login. The provider only sends a signed-in user here with such
// a next when their account can't finish it, so they must pick another one.
function isAuthorizeCallback(next: string | null): boolean {
  return !!next && next.startsWith('/authorize/callback')
}

export default function Login() {
  usePageTitle('Login')
  const { user, loading } = useAuth()
  const next = new URLSearchParams(window.location.search).get('next')
  const switchAccount = !loading && !!user && isAuthorizeCallback(next)
  const alreadyAuthed = !loading && !!user && !switchAccount

  const [passkeySupported] = useState(() => browserSupportsWebAuthn())
  const [passkeyBusy, setPasskeyBusy] = useState(false)
  const [passkeyError, setPasskeyError] = useState<string | null>(null)

  // `next` may be a backend-only route such as /authorize/callback, which the
  // SPA router doesn't know, so it needs a full navigation, not <Navigate>.
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

  const handlePasskeyLogin = async () => {
    setPasskeyError(null)
    setPasskeyBusy(true)
    try {
      const options = await passkeyLoginBegin(next ?? undefined)
      const response = await getPasskey(options)
      await passkeyLoginFinish(response)
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
          {switchAccount ? (
            <div className="alert alert-warning mb-6 text-left">
              <span>
                You're signed in as {user?.Email}, which can't sign in to this organization. Sign in
                with an account that belongs to it.
              </span>
            </div>
          ) : (
            <p className="mb-6 text-base-content/70">Sign in to continue.</p>
          )}

          <ErrorAlert error={passkeyError} />

          <div className="flex flex-col gap-2">
            {passkeySupported && (
              <>
                <button
                  type="button"
                  className="btn btn-primary"
                  disabled={passkeyBusy}
                  onClick={() => void handlePasskeyLogin()}
                >
                  <UserKey className="size-4" aria-hidden="true" />
                  Sign in with a passkey
                </button>
              </>
            )}
            <SSOButtons verb="Sign in" next={next} />
          </div>

          <p className="mt-6 text-sm">
            New here?{' '}
            <Link to={`/signup${nextQuery(next)}`} className="link link-primary">
              Create an account
            </Link>
          </p>
        </div>
      </div>
    </div>
  )
}
