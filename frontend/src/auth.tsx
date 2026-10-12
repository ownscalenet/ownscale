import { useEffect, useState, type ReactNode } from 'react'
import { AuthContext } from './authContext'
import {
  ApiError,
  errorMessage,
  getSession,
  UNAUTHORIZED_EVENT,
  type Organization,
  type User,
} from './api'

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

  // Unprovisioned and sleeping orgs stay that way until their next use, so
  // there's nothing to wait for.
  const pending =
    organization != null &&
    organization.Status !== 'active' &&
    organization.Status !== 'unprovisioned' &&
    organization.Status !== 'sleeping'
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
