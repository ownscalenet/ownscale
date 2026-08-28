import { Navigate, Route, Routes } from 'react-router-dom'
import { AuthProvider, useAuth } from './auth'
import Layout from './Layout'
import Login from './pages/Login'
import Dashboard from './pages/Dashboard'
import Machines from './pages/Machines'
import Users from './pages/Users'
import AccessControl from './pages/AccessControl'
import Settings from './pages/Settings'
import CreateOrganization from './pages/CreateOrganization'
import AcceptInvite from './pages/AcceptInvite'

function RequireAuth({ children }: { children: React.ReactElement }) {
  const { user, organization, loading } = useAuth()
  if (loading) {
    return (
      <div className="min-h-screen flex items-center justify-center">
        <span className="loading loading-spinner loading-lg"></span>
      </div>
    )
  }
  if (!user) {
    return <Navigate to="/login" replace />
  }
  // A user whose organization isn't active yet (no org, still pending, or
  // failed) belongs on the create-organization page: that's also where the
  // "provisioning..." polling UI lives, so a pending org stays visible
  // there even across a page refresh instead of bouncing into the app.
  if (!user.OrganizationID.Valid || organization?.Status !== 'active') {
    return <Navigate to="/create-organization" replace />
  }
  return children
}

function RequireAuthNoOrg({ children }: { children: React.ReactElement }) {
  const { user, organization, loading } = useAuth()
  if (loading) {
    return (
      <div className="min-h-screen flex items-center justify-center">
        <span className="loading loading-spinner loading-lg"></span>
      </div>
    )
  }
  if (!user) {
    return <Navigate to="/login" replace />
  }
  if (user.OrganizationID.Valid && organization?.Status === 'active') {
    return <Navigate to="/" replace />
  }
  return children
}

function AppRoutes() {
  return (
    <Routes>
      <Route path="/login" element={<Login />} />
      <Route path="/invite/:token" element={<AcceptInvite />} />
      <Route
        path="/create-organization"
        element={
          <RequireAuthNoOrg>
            <CreateOrganization />
          </RequireAuthNoOrg>
        }
      />
      <Route
        path="/"
        element={
          <RequireAuth>
            <Layout />
          </RequireAuth>
        }
      >
        <Route index element={<Dashboard />} />
        <Route path="machines" element={<Machines />} />
        <Route path="users" element={<Users />} />
        <Route path="access-control" element={<AccessControl />} />
        <Route path="settings" element={<Settings />} />
      </Route>
    </Routes>
  )
}

export default function App() {
  return (
    <AuthProvider>
      <AppRoutes />
    </AuthProvider>
  )
}
