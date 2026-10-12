import { Navigate, Route, Routes, useLocation } from 'react-router-dom'
import { AuthProvider } from './auth'
import { useAuth } from './authContext'
import Layout from './Layout'
import ConfirmProvider from './components/ConfirmProvider'
import Login from './pages/Login'
import Machines from './pages/Machines'
import MachineDetail from './pages/MachineDetail'
import SSH from './pages/SSH'
import Users from './pages/Users'
import AccessControl from './pages/AccessControl'
import AuthRestrictions from './pages/AuthRestrictions'
import DNS from './pages/DNS'
import Funnel from './pages/Funnel'
import Settings from './pages/Settings'
import UserSettings from './pages/UserSettings'
import AcceptInvite from './pages/AcceptInvite'
import { PageSpinner } from './components/ui'
import NotFound from './pages/NotFound'

function RequireAuth({ children }: { children: React.ReactElement }) {
  const { user, loading } = useAuth()
  const location = useLocation()
  if (loading) {
    return <PageSpinner fullScreen />
  }
  if (!user) {
    const next = encodeURIComponent(location.pathname + location.search)
    return <Navigate to={`/login?next=${next}`} replace />
  }
  return children
}

function AppRoutes() {
  return (
    <Routes>
      <Route path="/login" element={<Login />} />
      <Route path="/invite/:token" element={<AcceptInvite />} />
      <Route
        path="/"
        element={
          <RequireAuth>
            <Layout />
          </RequireAuth>
        }
      >
        <Route index element={<Navigate to="/machines" replace />} />
        <Route path="machines" element={<Machines />} />
        <Route path="machines/:ip" element={<MachineDetail />} />
        <Route path="machines/:id/ssh" element={<SSH />} />
        <Route path="users" element={<Users />} />
        <Route path="access-control" element={<AccessControl />} />
        <Route path="auth-restrictions" element={<AuthRestrictions />} />
        <Route path="dns" element={<DNS />} />
        <Route path="funnel" element={<Funnel />} />

        <Route path="settings" element={<Settings />} />
        <Route path="user-settings" element={<UserSettings />} />
      </Route>
      <Route path="*" element={<NotFound />} />
    </Routes>
  )
}

export default function App() {
  return (
    <AuthProvider>
      <ConfirmProvider>
        <AppRoutes />
      </ConfirmProvider>
    </AuthProvider>
  )
}
