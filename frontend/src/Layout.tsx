import { NavLink, Outlet } from 'react-router-dom'
import { useAuth } from './auth'
import { logout } from './api'

export default function Layout() {
  const { user, refresh } = useAuth()

  const handleLogout = async () => {
    await logout()
    await refresh()
  }

  return (
    <div className="drawer lg:drawer-open">
      <input id="sidebar-drawer" type="checkbox" className="drawer-toggle" />

      <div className="drawer-content flex flex-col">
        <div className="navbar bg-base-100 shadow-sm lg:hidden">
          <label htmlFor="sidebar-drawer" className="btn btn-square btn-ghost drawer-button">
            <svg xmlns="http://www.w3.org/2000/svg" className="h-6 w-6" fill="none" viewBox="0 0 24 24" stroke="currentColor">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M4 6h16M4 12h16M4 18h16" />
            </svg>
          </label>
          <span className="text-lg font-bold px-2">Admin Panel</span>
        </div>

        <main className="p-6 flex-1">
          <Outlet />
        </main>
      </div>

      <div className="drawer-side">
        <label htmlFor="sidebar-drawer" aria-label="close sidebar" className="drawer-overlay"></label>
        <aside className="bg-base-100 min-h-full w-64">
          <div className="p-4 text-xl font-bold border-b border-base-200">Admin Panel</div>
          <ul className="menu p-4 w-full">
            <li>
              <NavLink to="/" end className={({ isActive }) => (isActive ? 'active' : '')}>
                Dashboard
              </NavLink>
            </li>
            <li>
              <NavLink to="/machines" className={({ isActive }) => (isActive ? 'active' : '')}>
                Machines
              </NavLink>
            </li>
          </ul>
          {user && (
            <div className="p-4 border-t border-base-200 mt-auto">
              <p className="text-sm font-medium truncate">{user.Name}</p>
              <p className="text-xs text-base-content/60 truncate mb-2">{user.Email}</p>
              <button type="button" className="btn btn-sm btn-outline w-full" onClick={handleLogout}>
                Logout
              </button>
            </div>
          )}
        </aside>
      </div>
    </div>
  )
}
