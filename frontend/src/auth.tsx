import { createContext, useContext, useEffect, useState, type ReactNode } from 'react'
import { ApiError, getSession, UNAUTHORIZED_EVENT, type Organization, type User } from './api'

interface AuthState {
  user: User | null
  organization: Organization | null
  loading: boolean
  error: string | null
  refresh: () => Promise<void>
}

const AuthContext = createContext<AuthState | null>(null)

export function AuthProvider({ children }: { children: ReactNode }) {
  const [user, setUser] = useState<User | null>(null)
  const [organization, setOrganization] = useState<Organization | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  const refresh = async () => {
    setLoading(true)
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
        setError(err instanceof Error ? err.message : String(err))
      }
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    void refresh()
  }, [])

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
