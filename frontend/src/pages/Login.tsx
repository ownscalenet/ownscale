import { useEffect } from 'react'
import { Navigate } from 'react-router-dom'
import { useAuth } from '../auth'
import { usePageTitle } from '../hooks/usePageTitle'

// isSafeRelativeNext guards against open redirects: only a same-origin,
// path-absolute URL (no scheme-relative "//host" tricks) is followed.
function isSafeRelativeNext(next: string | null): next is string {
  return !!next && next.startsWith('/') && !next.startsWith('//')
}

export default function Login() {
  usePageTitle('Login')
  const { user, loading } = useAuth()
  const next = new URLSearchParams(window.location.search).get('next')
  const nextParam = next ? `?next=${encodeURIComponent(next)}` : ''
  const href = `/auth/oidc${nextParam}`
  const googleHref = `/auth/google${nextParam}`
  const microsoftHref = `/auth/microsoft${nextParam}`
  const alreadyAuthed = !loading && !!user

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

  return (
    <div className="hero min-h-screen bg-base-200">
      <div className="hero-content text-center">
        <div className="max-w-md">
          <h1 className="text-3xl font-bold mb-2">Ownscale console</h1>
          <p className="mb-6 text-base-content/70">Sign in to continue.</p>
          <div className="flex flex-col gap-2">
            <a href={href} className="btn btn-primary">
              Sign in with SSO
            </a>
            <a href={googleHref} className="btn btn-outline">
              Sign in with Google
            </a>
            <a href={microsoftHref} className="btn btn-outline">
              Sign in with Microsoft
            </a>
          </div>
        </div>
      </div>
    </div>
  )
}
