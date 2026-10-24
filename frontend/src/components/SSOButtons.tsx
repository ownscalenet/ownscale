import type { ComponentType } from 'react'
import { useQuery } from '@tanstack/react-query'
import { KeyRound } from 'lucide-react'
import { getAuthProviders, type AuthProviders } from '../api'
import { nextQuery } from '../lib/authRedirect'
import { GitHubIcon, GoogleIcon, MicrosoftIcon } from './ProviderIcons'

// ssoButtons maps each AuthProviders key to its button copy and icon.
const ssoButtons: Record<
  keyof AuthProviders,
  { label: string; Icon: ComponentType<{ className?: string }> }
> = {
  github: { label: 'GitHub', Icon: GitHubIcon },
  google: { label: 'Google', Icon: GoogleIcon },
  microsoft: { label: 'Microsoft', Icon: MicrosoftIcon },
  oidc: { label: 'SSO', Icon: KeyRound },
}

const providers: (keyof AuthProviders)[] = ['github', 'google', 'microsoft', 'oidc']

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
    const { label, Icon } = ssoButtons[provider]
    if (!authProviders?.[provider]) {
      return (
        <button
          key={provider}
          type="button"
          className="btn btn-outline btn-disabled"
          disabled
          title={`${label} sign-in is not enabled for this deployment.`}
        >
          {/* The brand marks' own colors would ignore the disabled look. */}
          <Icon className="size-4 grayscale opacity-50" />
          {verb} with {label}
        </button>
      )
    }
    return (
      <a key={provider} href={`/auth/${provider}${nextQuery(next)}`} className="btn btn-outline">
        <Icon className="size-4" />
        {verb} with {label}
      </a>
    )
  })
}
