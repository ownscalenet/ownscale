import { Navigate, useParams } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { useAuth } from '../auth'
import { errorMessage, getInvitePreview } from '../api'
import { usePageTitle } from '../hooks/usePageTitle'
import { ErrorAlert, PageSpinner } from '../components/ui'

// AcceptInvite is the /invite/:token landing page. An unauthenticated
// visitor sees a preview and a login link (next=/invite/:token), so they
// come back here once signed in.
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

  const loginHref = `/login?next=${encodeURIComponent(`/invite/${token}`)}`
  const signedOut = !user

  // Without a valid invite there is nothing to preview, so go straight to login.
  if (signedOut && !query.isPending && !preview?.valid) {
    return <Navigate to={loginHref} replace />
  }

  const orgSubdomain = organization?.Domain.split('.')[0]
  const joinedInvitedOrg = !signedOut && !!preview && orgSubdomain === preview.organizationName

  // A successful join needs no further confirmation step -- send the user
  // straight into the console.
  if (joinedInvitedOrg) {
    return <Navigate to="/" replace />
  }

  return (
    <div className="hero min-h-screen bg-base-200">
      <div className="hero-content w-full">
        <div className="card bg-base-100 border border-base-300 w-full max-w-md">
          <div className="card-body">
            <h1 className="text-2xl font-semibold">Join organization</h1>

            <ErrorAlert error={error} />

            {preview ? (
              <>
                <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-2 text-sm">
                  {preview.invitedBy && (
                    <>
                      <dt className="text-base-content/60">Invited by</dt>
                      <dd>{preview.invitedBy}</dd>
                    </>
                  )}
                  <dt className="text-base-content/60">Organization</dt>
                  <dd className="font-mono break-all">{preview.organizationDomain}</dd>
                  <dt className="text-base-content/60">Role</dt>
                  <dd className="capitalize">{preview.role.replace(/_/g, ' ')}</dd>
                </dl>
                {signedOut ? (
                  <a href={loginHref} className="btn btn-primary">
                    Sign in to join
                  </a>
                ) : (
                  <p className="text-sm text-base-content/70">
                    This invite doesn&rsquo;t apply to your account (it may be for a different
                    organization, already used, or no longer valid).
                  </p>
                )}
              </>
            ) : (
              !error && <span className="loading loading-spinner"></span>
            )}
          </div>
        </div>
      </div>
    </div>
  )
}
