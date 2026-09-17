import { useRef } from 'react'
import { NavLink, Outlet } from 'react-router-dom'
import { useAuth } from './auth'
import { logout } from './api'

export default function Layout() {
  const { user, organization, refresh } = useAuth()
  const drawerToggleRef = useRef<HTMLInputElement>(null)

  const handleLogout = async () => {
    await logout()
    await refresh()
  }

  // Closes the mobile drawer after a nav click; lg:drawer-open keeps it
  // permanently open on desktop, where this is a no-op.
  const closeDrawer = () => {
    if (drawerToggleRef.current) {
      drawerToggleRef.current.checked = false
    }
  }

  return (
    <div className="drawer lg:drawer-open">
      <input id="sidebar-drawer" ref={drawerToggleRef} type="checkbox" className="drawer-toggle" />

      <div className="drawer-content flex flex-col">
        <div className="navbar bg-base-100 shadow-sm lg:hidden">
          <label htmlFor="sidebar-drawer" className="btn btn-square btn-ghost drawer-button">
            <svg
              xmlns="http://www.w3.org/2000/svg"
              className="h-6 w-6"
              fill="none"
              viewBox="0 0 24 24"
              stroke="currentColor"
            >
              <path
                strokeLinecap="round"
                strokeLinejoin="round"
                strokeWidth={2}
                d="M4 6h16M4 12h16M4 18h16"
              />
            </svg>
          </label>
          <span className="text-lg font-bold px-2">Ownscale console</span>
        </div>

        <main className="p-6 flex-1">
          <Outlet />
        </main>
      </div>

      <div className="drawer-side">
        <label
          htmlFor="sidebar-drawer"
          aria-label="close sidebar"
          className="drawer-overlay"
        ></label>
        <aside className="bg-base-100 min-h-full w-64 flex flex-col">
          <div className="p-4 text-xl font-bold border-b border-base-200">Ownscale console</div>
          <ul className="menu p-4 w-full" onClick={closeDrawer}>
            <li>
              <NavLink to="/machines" className={({ isActive }) => (isActive ? 'active' : '')}>
                Machines
              </NavLink>
            </li>
            <li>
              <NavLink to="/users" className={({ isActive }) => (isActive ? 'active' : '')}>
                Users
              </NavLink>
            </li>
            <li>
              <NavLink
                to="/access-control"
                className={({ isActive }) => (isActive ? 'active' : '')}
              >
                Access Control
              </NavLink>
            </li>
            <li>
              <NavLink
                to="/auth-restrictions"
                className={({ isActive }) => (isActive ? 'active' : '')}
              >
                Auth Restrictions
              </NavLink>
            </li>
            <li>
              <NavLink to="/dns" className={({ isActive }) => (isActive ? 'active' : '')}>
                DNS
              </NavLink>
            </li>
            <li>
              <NavLink to="/funnel" className={({ isActive }) => (isActive ? 'active' : '')}>
                Funnel
              </NavLink>
            </li>
            <li>
              <NavLink to="/settings" className={({ isActive }) => (isActive ? 'active' : '')}>
                Settings
              </NavLink>
            </li>
          </ul>
          <div className="mt-auto border-t border-base-200">
            <ul className="menu p-4 w-full" onClick={closeDrawer}>
              <li>
                <NavLink
                  to="/user-settings"
                  className={({ isActive }) => (isActive ? 'active' : '')}
                >
                  User settings
                </NavLink>
              </li>
            </ul>
            {user && (
              <div className="p-4 pt-0">
                <p className="text-sm font-medium truncate">{user.Name}</p>
                <p className="text-xs text-base-content/60 truncate">{user.Email}</p>
                {organization && (
                  <div className="mt-2 mb-2">
                    <p className="text-xs text-base-content/60 truncate">{organization.Domain}</p>
                    <span
                      className={`badge badge-xs ${
                        organization.Status === 'active' ? 'badge-success' : 'badge-warning'
                      }`}
                    >
                      {organization.Status}
                    </span>
                  </div>
                )}
                <button
                  type="button"
                  className="btn btn-sm btn-outline w-full mt-2"
                  onClick={handleLogout}
                >
                  Logout
                </button>
              </div>
            )}
          </div>
        </aside>
      </div>
    </div>
  )
}
