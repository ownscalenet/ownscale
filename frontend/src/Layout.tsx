import { useRef, type ReactNode } from 'react'
import { NavLink, Outlet, useLocation } from 'react-router-dom'
import {
  BookOpen,
  GlobeLock,
  ListChecks,
  LogOut,
  Menu,
  Monitor,
  Network,
  Settings as SettingsIcon,
  ShieldCheck,
  UserCog,
  Users,
} from 'lucide-react'
import { useAuth } from './authContext'
import { displayName, isPasskeyEmail, logout } from './api'
import { CopyIcon } from './components/CopyButton'
import Logo from './components/Logo'
import ThemeToggle from './components/ThemeToggle'
import { docsURL } from './lib/faq'

// navGroups are the sidebar's pages. NavLink marks the current one active.
const navGroups: { title: string; links: { to: string; label: string; icon: ReactNode }[] }[] = [
  {
    title: 'Network',
    links: [
      { to: '/machines', label: 'Machines', icon: <Monitor /> },
      { to: '/funnels', label: 'Funnels', icon: <GlobeLock /> },
      { to: '/dns', label: 'DNS', icon: <Network /> },
    ],
  },
  {
    title: 'Access',
    links: [
      { to: '/users', label: 'Users', icon: <Users /> },
      { to: '/access-control', label: 'Access Control', icon: <ListChecks /> },
      { to: '/auth-restrictions', label: 'Auth Restrictions', icon: <ShieldCheck /> },
      { to: '/pre-auth-keys', label: 'Pre-auth Keys', icon: <SettingsIcon /> },
    ],
  },
]

const userSettingsLink = { to: '/user-settings', label: 'User settings', icon: <UserCog /> }

const statusDot: Record<string, string> = {
  active: 'bg-success',
  unavailable: 'bg-error',
  update_pending: 'bg-warning',
}

function initials(name: string): string {
  return (
    name
      .split(/\s+/)
      .filter(Boolean)
      .slice(0, 2)
      .map((w) => w[0].toUpperCase())
      .join('') || '?'
  )
}

const iconClass = '[&_svg]:size-4 [&_svg]:shrink-0'

export default function Layout() {
  const { user, organization, refresh } = useAuth()
  const drawerToggleRef = useRef<HTMLInputElement>(null)
  const { pathname } = useLocation()

  const links = [...navGroups.flatMap((g) => g.links), userSettingsLink]
  const pageTitle = links.find((l) => pathname.startsWith(l.to))?.label ?? 'Ownscale'
  const loginServer = organization ? `${window.location.protocol}//${organization.Domain}` : ''

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

  const navItem = (l: (typeof links)[number]) => (
    <li key={l.to}>
      <NavLink
        to={l.to}
        className={({ isActive }) =>
          `${iconClass} flex items-center gap-2.5 rounded-field px-2.5 py-1.5 text-sm font-medium border ${
            isActive
              ? 'bg-neutral text-neutral-content border-base-300'
              : 'border-transparent text-base-content/70 hover:text-base-content hover:bg-base-200'
          }`
        }
      >
        {l.icon}
        {l.label}
      </NavLink>
    </li>
  )

  return (
    <div className="drawer lg:drawer-open">
      <input id="sidebar-drawer" ref={drawerToggleRef} type="checkbox" className="drawer-toggle" />

      <div className="drawer-content flex flex-col">
        <div className="navbar sticky top-0 z-10 bg-base-100 border-b border-base-300 lg:hidden">
          <label
            htmlFor="sidebar-drawer"
            aria-label="open sidebar"
            className="btn btn-square btn-ghost drawer-button"
          >
            <Menu className="size-5" />
          </label>
          <div className="min-w-0 px-2">
            <div className="text-sm font-semibold leading-tight">{pageTitle}</div>
            {organization && (
              <div className="font-mono text-xs text-base-content/60 truncate">
                {organization.Domain}
              </div>
            )}
          </div>
        </div>

        <main className="p-6 flex-1 w-full max-w-[1360px] mx-auto">
          <Outlet />
        </main>
      </div>

      <div className="drawer-side z-20">
        <label
          htmlFor="sidebar-drawer"
          aria-label="close sidebar"
          className="drawer-overlay"
        ></label>
        <aside className="bg-base-100 border-r border-base-300 min-h-full w-64 flex flex-col">
          <div className="p-4 border-b border-base-300">
            <div className="flex items-center gap-2.5">
              <Logo className="size-7" />
              <span className="text-sm font-semibold">Ownscale</span>
            </div>
            {organization && (
              <div className="mt-3 rounded-field border border-base-300 bg-base-200 px-2.5 py-2">
                <div className="flex items-center gap-1.5 text-[11px] uppercase tracking-wider text-base-content/60">
                  <span
                    className={`size-1.5 rounded-full ${statusDot[organization.Status] ?? 'bg-base-content/30'}`}
                    aria-hidden="true"
                  />
                  Login server
                </div>
                <div className="mt-1 flex items-center gap-1">
                  <span
                    className="font-mono text-sm font-medium truncate flex-1"
                    title={organization.Domain}
                  >
                    {organization.Domain}
                  </span>
                  <CopyIcon text={loginServer} label="login server" />
                </div>
              </div>
            )}
          </div>

          <nav className="p-3 flex-1 overflow-y-auto" onClick={closeDrawer}>
            {navGroups.map((g) => (
              <div key={g.title} className="mb-4">
                <div className="px-2.5 pb-1 text-[11px] font-medium uppercase tracking-wider text-base-content/50">
                  {g.title}
                </div>
                <ul className="flex flex-col gap-0.5">{g.links.map(navItem)}</ul>
              </div>
            ))}
          </nav>

          <div className="border-t border-base-300 p-3">
            <ul onClick={closeDrawer} className="flex flex-col gap-0.5">
              {navItem(userSettingsLink)}
              <li>
                <a
                  href={docsURL()}
                  target="_blank"
                  rel="noreferrer"
                  className={`${iconClass} flex items-center gap-2.5 rounded-field px-2.5 py-1.5 text-sm font-medium border border-transparent text-base-content/70 hover:text-base-content hover:bg-base-200`}
                >
                  <BookOpen />
                  Documentation
                </a>
              </li>
            </ul>
            {user && (
              <div className="mt-3 flex items-center gap-2.5 px-1">
                <div
                  className="size-8 shrink-0 rounded-full bg-base-300 grid place-items-center text-xs font-semibold"
                  aria-hidden="true"
                >
                  {initials(displayName(user))}
                </div>
                <div className="min-w-0 flex-1">
                  <p className="text-sm font-medium truncate">{displayName(user)}</p>
                  {!isPasskeyEmail(user.Email) && (
                    <p className="text-xs text-base-content/60 truncate">{user.Email}</p>
                  )}
                </div>
                <ThemeToggle />
                <button
                  type="button"
                  className="btn btn-ghost btn-sm btn-square"
                  aria-label="Logout"
                  title="Logout"
                  onClick={handleLogout}
                >
                  <LogOut className="size-4" />
                </button>
              </div>
            )}
          </div>
        </aside>
      </div>
    </div>
  )
}
