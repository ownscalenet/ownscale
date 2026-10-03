import { useEffect, useState } from 'react'
import { Navigate, useParams } from 'react-router-dom'
import { useAuth } from '../auth'
import { getInvitePreview, isAbortError, type InvitePreview } from '../api'
import { usePageTitle } from '../hooks/usePageTitle'

// AcceptInvite is the /invite/:token landing page. An unauthenticated
// visitor is sent through /login?next=/invite/:token, so they come back here
// once signed in.
export default function AcceptInvite() {
  usePageTitle('Accept Invite')
  const { token } = useParams<{ token: string }>()
  const { user, organization, loading } = useAuth()

  const [preview, setPreview] = useState<InvitePreview | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    if (!token) return
    const controller = new AbortController()
    getInvitePreview(token, controller.signal)
      .then(setPreview)
      .catch((err) => {
        if (isAbortError(err)) return
        setError(err instanceof Error ? err.message : String(err))
      })
    return () => controller.abort()
  }, [token])

  if (loading) {
    return (
      <div className="min-h-screen flex items-center justify-center">
        <span className="loading loading-spinner loading-lg"></span>
      </div>
    )
  }

  if (!user) {
    return <Navigate to={`/login?next=${encodeURIComponent(`/invite/${token}`)}`} replace />
  }

  const orgSubdomain = organization?.Domain.split('.')[0]
  const joinedInvitedOrg = !!preview && orgSubdomain === preview.organizationName

  // A successful join needs no further confirmation step -- send the user
  // straight into the console.
  if (joinedInvitedOrg) {
    return <Navigate to="/" replace />
  }

  return (
    <div className="hero min-h-screen bg-base-200">
      <div className="hero-content text-center">
        <div className="max-w-md">
          <h1 className="text-3xl font-bold mb-2">Join organization</h1>

          {error && (
            <div className="alert alert-error mb-4">
              <span>{error}</span>
            </div>
          )}

          {preview ? (
            <p className="text-base-content/70">
              This invite doesn&rsquo;t apply to your account (it may be for a different
              organization, already used, or no longer valid).
            </p>
          ) : (
            !error && <span className="loading loading-spinner"></span>
          )}
        </div>
      </div>
    </div>
  )
}
