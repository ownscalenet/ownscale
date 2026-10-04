import { createContext, useContext, useEffect, useState, type ReactNode } from 'react'
import {
  ApiError,
  errorMessage,
  getSession,
  UNAUTHORIZED_EVENT,
  type Organization,
  type User,
} from './api'

interface AuthState {
  user: User | null
  organization: Organization | null
  loading: boolean
  error: string | null
  refresh: () => Promise<void>
}

const AuthContext = createContext<AuthState | null>(null)

// pendingPollMs is how often the session is re-read while the organization
// isn't active, so its status doesn't stay stale.
const pendingPollMs = 3_000

export function AuthProvider({ children }: { children: ReactNode }) {
  const [user, setUser] = useState<User | null>(null)
  const [organization, setOrganization] = useState<Organization | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  // Only the first load sets loading: RequireAuth swaps the whole layout for
  // a spinner while it's set.
  const refresh = async () => {
    setError(null)
    try {
      const { user, organization } = await getSession()
      setUser(user)
      setOrganization(organization)
    } catch (err) {
      if (err instanceof ApiError && err.status === 401) {
        setUser(null)
        setOrganization(null)
      } else {
        setError(errorMessage(err))
      }
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    void refresh()
  }, [])

  // An unprovisioned org stays that way until its first use, so there's
  // nothing to wait for.
  const pending =
    organization != null &&
    organization.Status !== 'active' &&
    organization.Status !== 'unprovisioned'
  useEffect(() => {
    if (!pending) return
    const timer = setInterval(() => void refresh(), pendingPollMs)
    return () => clearInterval(timer)
  }, [pending])

  // A 401 on any other API call means the session expired mid-page; clear it
  // here so RequireAuth's existing "no user -> redirect to /login" logic
  // kicks in, instead of leaving stale user state around.
  useEffect(() => {
    const onUnauthorized = () => {
      setUser(null)
      setOrganization(null)
    }
    window.addEventListener(UNAUTHORIZED_EVENT, onUnauthorized)
    return () => window.removeEventListener(UNAUTHORIZED_EVENT, onUnauthorized)
  }, [])

  return (
    <AuthContext.Provider value={{ user, organization, loading, error, refresh }}>
      {children}
    </AuthContext.Provider>
  )
}

export function useAuth(): AuthState {
  const ctx = useContext(AuthContext)
  if (!ctx) throw new Error('useAuth must be used within AuthProvider')
  return ctx
}
