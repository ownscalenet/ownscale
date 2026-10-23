import { useQuery } from '@tanstack/react-query'
import { getAuthProviders, type AuthProviders } from '../api'
import { nextQuery } from '../lib/authRedirect'

// ssoLabels maps each AuthProviders key to its button copy.
const ssoLabels: Record<keyof AuthProviders, string> = {
  oidc: 'SSO',
  google: 'Google',
  microsoft: 'Microsoft',
}

const providers: (keyof AuthProviders)[] = ['oidc', 'google', 'microsoft']

// SSOButtons renders a link per upstream login provider. Signing in and
// signing up are the same flow (the account is created on first login), so
// verb only changes the copy. Unconfigured providers get disabled buttons
// with a tooltip rather than dead links; a failed fetch counts as nothing
// configured.
export default function SSOButtons({
  verb,
  next,
}: {
  verb: 'Sign in' | 'Sign up'
  next: string | null
}) {
  const { data: authProviders } = useQuery({
    queryKey: ['authProviders'],
    queryFn: getAuthProviders,
    retry: false,
  })

  return providers.map((provider) => {
    const label = ssoLabels[provider]
    if (!authProviders?.[provider]) {
      return (
        <button
          key={provider}
          type="button"
          className="btn btn-outline btn-disabled"
          disabled
          title={`${label} sign-in is not enabled for this deployment.`}
        >
          {verb} with {label}
        </button>
      )
    }
    return (
      <a key={provider} href={`/auth/${provider}${nextQuery(next)}`} className="btn btn-outline">
        {verb} with {label}
      </a>
    )
  })
}
