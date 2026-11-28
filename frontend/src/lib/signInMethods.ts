import type { ComponentType } from 'react'
import { KeyRound } from 'lucide-react'
import type { AuthProviders } from '../api'
import { GitHubIcon, GoogleIcon, MicrosoftIcon } from '../components/ProviderIcons'

type Method = { label: string; Icon: ComponentType<{ className?: string }> }

// SSOProvider excludes 'passkey': it has its own dedicated button/form on
// Login and Signup instead of a generic "Sign in with X" link.
export type SSOProvider = Exclude<keyof AuthProviders, 'passkey'>

// ssoButtons maps each SSOProvider to its button copy and icon.
export const ssoButtons: Record<SSOProvider, Method> = {
  github: { label: 'GitHub', Icon: GitHubIcon },
  google: { label: 'Google', Icon: GoogleIcon },
  microsoft: { label: 'Microsoft', Icon: MicrosoftIcon },
  oidc: { label: 'SSO', Icon: KeyRound },
}

// signInMethods labels every User.Provider, passkey accounts included.
export const signInMethods: Record<string, Method> = {
  ...ssoButtons,
  passkey: { label: 'Passkey', Icon: KeyRound },
}
