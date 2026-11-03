import { createContext, useContext } from 'react'
import type { Organization, User } from './api'

export interface AuthState {
  user: User | null
  organization: Organization | null
  version: string | null
  loading: boolean
  error: string | null
  refresh: () => Promise<void>
}

export const AuthContext = createContext<AuthState | null>(null)

export function useAuth(): AuthState {
  const ctx = useContext(AuthContext)
  if (!ctx) throw new Error('useAuth must be used within AuthProvider')
  return ctx
}
