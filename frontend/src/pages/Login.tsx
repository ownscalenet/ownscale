import { useEffect, useState } from 'react'
import { Navigate } from 'react-router-dom'
import { useAuth } from '../auth'
import {
  ApiError,
  passkeyLoginBegin,
  passkeyLoginFinish,
  passkeyRegisterBegin,
  passkeyRegisterFinish,
} from '../api'
import { createPasskey, getPasskey, browserSupportsWebAuthn } from '../lib/webauthn'
import { usePageTitle } from '../hooks/usePageTitle'

// isSafeRelativeNext guards against open redirects: only a same-origin,
// path-absolute URL (no scheme-relative "//host" tricks) is followed.
function isSafeRelativeNext(next: string | null): next is string {
  return !!next && next.startsWith('/') && !next.startsWith('//')
}

// goToNext mirrors the redirect logic handlers.finishLogin performs
// server-side (see handlers/auth.go): the passkey finish endpoints already
// redirect the underlying fetch(), but that doesn't move the browser's own
// location, so the SPA repeats the same "next-or-home" decision itself once
// the fetch succeeds.
// isAuthorizeCallback reports whether next finishes another organization's
// headscale SSO login. The provider only sends a signed-in user here with such
// a next when their account can't finish it, so they must pick another one.
function isAuthorizeCallback(next: string | null): boolean {
  return !!next && next.startsWith('/authorize/callback')
}

function goToNext(next: string | null) {
  window.location.assign(isSafeRelativeNext(next) ? next : '/')
}

export default function Login() {
  usePageTitle('Login')
  const { user, loading } = useAuth()
  const next = new URLSearchParams(window.location.search).get('next')
  const nextParam = next ? `?next=${encodeURIComponent(next)}` : ''
  const href = `/auth/oidc${nextParam}`
  const googleHref = `/auth/google${nextParam}`
  const microsoftHref = `/auth/microsoft${nextParam}`
  const switchAccount = !loading && !!user && isAuthorizeCallback(next)
  const alreadyAuthed = !loading && !!user && !switchAccount

  const [passkeySupported] = useState(() => browserSupportsWebAuthn())
  const [passkeyBusy, setPasskeyBusy] = useState(false)
  const [passkeyError, setPasskeyError] = useState<string | null>(null)
  const [showRegister, setShowRegister] = useState(false)
  const [email, setEmail] = useState('')

  // `next` may point at a backend-only route such as
  // /authorize/callback?id=... (used by the OIDC provider to finish an
  // authorization request for another organization's headscale SSO
  // login). That route isn't part of the SPA's client-side router, so
  // it must be reached via a real HTTP navigation, not React Router's
  // <Navigate>, or the browser is left on a blank page that matches no
  // route. A full navigation is used whenever `next` is present; plain "no
  // next" (e.g. visiting /login directly while already signed in) can stay
  // as an in-app client-side redirect to "/".
  useEffect(() => {
    if (alreadyAuthed && isSafeRelativeNext(next)) {
      window.location.replace(next)
    }
  }, [alreadyAuthed, next])

  if (alreadyAuthed) {
    if (isSafeRelativeNext(next)) {
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
      setPasskeyError(err instanceof Error ? err.message : String(err))
      setPasskeyBusy(false)
    }
  }

  const handlePasskeyRegister = async (e: React.FormEvent) => {
    e.preventDefault()
    setPasskeyError(null)
    setPasskeyBusy(true)
    try {
      const options = await passkeyRegisterBegin(email, next ?? undefined)
      const response = await createPasskey(options)
      await passkeyRegisterFinish(response)
      goToNext(next)
    } catch (err) {
      setPasskeyError(
        err instanceof ApiError ? err.message : err instanceof Error ? err.message : String(err),
      )
      setPasskeyBusy(false)
    }
  }

  return (
    <div className="hero min-h-screen bg-base-200">
      <div className="hero-content text-center">
        <div className="max-w-md">
          <h1 className="text-3xl font-bold mb-2">Ownscale console</h1>
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

          {passkeyError && (
            <div className="alert alert-error mb-4">
              <span>{passkeyError}</span>
            </div>
          )}

          <div className="flex flex-col gap-2">
            {passkeySupported && (
              <button
                type="button"
                className="btn btn-primary"
                disabled={passkeyBusy}
                onClick={() => void handlePasskeyLogin()}
              >
                Sign in with a passkey
              </button>
            )}
            <a href={href} className="btn btn-outline">
              Sign in with SSO
            </a>
            <a href={googleHref} className="btn btn-outline">
              Sign in with Google
            </a>
            <a href={microsoftHref} className="btn btn-outline">
              Sign in with Microsoft
            </a>
          </div>

          {passkeySupported && (
            <div className="mt-6 text-left">
              {!showRegister ? (
                <button
                  type="button"
                  className="btn btn-link btn-sm"
                  onClick={() => setShowRegister(true)}
                >
                  New here? Create an account with a passkey
                </button>
              ) : (
                <form
                  className="flex flex-col gap-2"
                  onSubmit={(e) => void handlePasskeyRegister(e)}
                >
                  <label className="fieldset-label" htmlFor="passkey-email">
                    Email
                  </label>
                  <input
                    id="passkey-email"
                    type="email"
                    required
                    autoComplete="email"
                    className="input input-bordered w-full"
                    placeholder="you@example.com"
                    value={email}
                    onChange={(e) => setEmail(e.target.value)}
                  />
                  <button type="submit" className="btn btn-primary" disabled={passkeyBusy}>
                    Continue with passkey
                  </button>
                </form>
              )}
            </div>
          )}
        </div>
      </div>
    </div>
  )
}
