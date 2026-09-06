import { useEffect, useState } from 'react'
import { Navigate, useNavigate, useParams } from 'react-router-dom'
import { useAuth } from '../auth'
import { acceptInvite, getInvitePreview, type InvitePreview } from '../api'
import { usePageTitle } from '../hooks/usePageTitle'

// AcceptInvite is the /invite/:token landing page. An unauthenticated
// visitor is sent through /login?next=/invite/:token (mirroring Login.tsx's
// existing next= redirect pattern) so they come right back here once
// signed in; a visitor who already belongs to an organization can't accept
// (ownscale supports one organization per user today).
export default function AcceptInvite() {
  usePageTitle('Accept Invite')
  const { token } = useParams<{ token: string }>()
  const { user, organization, loading, refresh } = useAuth()
  const navigate = useNavigate()

  const [preview, setPreview] = useState<InvitePreview | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [accepting, setAccepting] = useState(false)

  useEffect(() => {
    if (!token) return
    getInvitePreview(token)
      .then(setPreview)
      .catch((err) => setError(err instanceof Error ? err.message : String(err)))
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

  const alreadyInOrg = user.OrganizationID.Valid && organization?.Status === 'active'

  const handleAccept = async () => {
    if (!token) return
    setAccepting(true)
    setError(null)
    try {
      await acceptInvite(token)
      await refresh()
      navigate('/')
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setAccepting(false)
    }
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

          {alreadyInOrg ? (
            <p className="text-base-content/70">
              You already belong to an organization. You must leave it before accepting a new
              invite.
            </p>
          ) : preview ? (
            preview.valid ? (
              <>
                <p className="mb-6 text-base-content/70">
                  You&rsquo;ve been invited to join <strong>{preview.organizationName}</strong> as{' '}
                  <strong>{preview.role.replace('_', ' ')}</strong>.
                </p>
                <button
                  type="button"
                  className="btn btn-primary"
                  disabled={accepting}
                  onClick={() => void handleAccept()}
                >
                  Accept invite
                </button>
              </>
            ) : (
              <p className="text-base-content/70">This invite is no longer valid.</p>
            )
          ) : (
            !error && <span className="loading loading-spinner"></span>
          )}
        </div>
      </div>
    </div>
  )
}
