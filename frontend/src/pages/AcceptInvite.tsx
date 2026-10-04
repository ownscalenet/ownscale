import { Navigate, useParams } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { useAuth } from '../auth'
import { errorMessage, getInvitePreview } from '../api'
import { usePageTitle } from '../hooks/usePageTitle'
import { ErrorAlert, PageSpinner } from '../components/ui'

// AcceptInvite is the /invite/:token landing page. An unauthenticated
// visitor is sent through /login?next=/invite/:token, so they come back here
// once signed in.
export default function AcceptInvite() {
  usePageTitle('Accept Invite')
  const { token } = useParams<{ token: string }>()
  const { user, organization, loading } = useAuth()

  const query = useQuery({
    queryKey: ['invite', token],
    queryFn: ({ signal }) => getInvitePreview(token!, signal),
    enabled: !!token,
  })
  const preview = query.data ?? null
  const error = query.error ? errorMessage(query.error) : null

  if (loading) {
    return <PageSpinner fullScreen />
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

          <ErrorAlert error={error} />

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
