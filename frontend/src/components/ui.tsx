import { useState, type ReactNode } from 'react'
import type { ExitNodeStatus } from '../api'

// PageHeader is a page's title, with an optional subtitle and the page's
// main actions next to it.
export function PageHeader({
  title,
  subtitle,
  children,
}: {
  title: string
  subtitle?: ReactNode
  children?: ReactNode
}) {
  return (
    <div className="flex flex-wrap items-center justify-between gap-3 mb-5">
      <div className="min-w-0">
        <h1 className="text-[22px] font-semibold tracking-tight">{title}</h1>
        {subtitle && <p className="text-sm text-base-content/60 mt-0.5">{subtitle}</p>}
      </div>
      {children}
    </div>
  )
}

const statusStyles = {
  online: { badge: 'badge-success badge-soft', marker: 'size-1.5 rounded-full bg-current' },
  offline: {
    badge: 'badge-outline text-base-content/60',
    marker: 'size-1.5 rounded-full border border-current',
  },
  expired: {
    badge: 'badge-warning badge-soft',
    marker: 'size-1.5 rotate-45 bg-current',
  },
}

// StatusBadge shows a machine status by marker shape and label, not color alone.
export function StatusBadge({ status }: { status: keyof typeof statusStyles }) {
  const st = statusStyles[status]
  return (
    <span className={`badge badge-sm gap-1.5 ${st.badge}`}>
      <span className={st.marker} aria-hidden="true" />
      {status[0].toUpperCase() + status.slice(1)}
    </span>
  )
}

// ExitNodeBadge marks a machine that advertises itself as an exit node, and
// nothing when it doesn't.
export function ExitNodeBadge({ status }: { status: ExitNodeStatus }) {
  if (status === 'none') return null
  if (status === 'approved') return <span className="badge badge-primary badge-sm">Exit node</span>
  return (
    <span
      className="badge badge-warning badge-soft badge-sm"
      title="Approve it from Edit route settings"
    >
      Exit node · awaiting approval
    </span>
  )
}

// ErrorAlert shows error, and nothing when there is none. It is a fixed
// toast so it stays visible however far the page is scrolled; inline keeps it
// in the page flow, for when the error is the page's content.
export function ErrorAlert({
  error,
  inline = false,
}: {
  error: string | null
  inline?: boolean
}) {
  const [dismissed, setDismissed] = useState<string | null>(null)
  // Forget the dismissal once the error is gone, so the same error shows again.
  if (!error && dismissed !== null) setDismissed(null)

  if (!error) return null
  const message = (
    <span className={`whitespace-pre-wrap break-words ${error.includes('\n') ? 'font-mono text-sm' : ''}`}>
      {error}
    </span>
  )
  if (inline) {
    return (
      <div role="alert" className="alert alert-error mb-4">
        {message}
      </div>
    )
  }
  if (dismissed === error) return null
  return (
    // Above modals, so errors raised inside a dialog are not hidden.
    <div className="toast toast-top toast-end z-[1000]">
      <div role="alert" className="alert alert-error max-w-md">
        {message}
        <button
          type="button"
          className="btn btn-ghost btn-xs"
          aria-label="Dismiss error"
          onClick={() => setDismissed(error)}
        >
          ✕
        </button>
      </div>
    </div>
  )
}

// PageSpinner stands in for a page's content while it loads; fullScreen
// fills the window for pages shown outside the console layout.
export function PageSpinner({ fullScreen = false }: { fullScreen?: boolean }) {
  return (
    <div className={`flex items-center justify-center ${fullScreen ? 'min-h-screen' : 'h-64'}`}>
      <span className="loading loading-spinner loading-lg"></span>
    </div>
  )
}

// EmptyRow is a table's only row when there's nothing to list.
export function EmptyRow({ colSpan, children }: { colSpan: number; children: ReactNode }) {
  return (
    <tr>
      <td colSpan={colSpan} className="text-sm text-base-content/60">
        {children}
      </td>
    </tr>
  )
}
