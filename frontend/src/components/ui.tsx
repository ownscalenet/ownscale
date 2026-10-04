import type { ReactNode } from 'react'

// PageHeader is a page's title, with the page's main actions next to it.
export function PageHeader({ title, children }: { title: string; children?: ReactNode }) {
  return (
    <div className="flex items-center justify-between mb-4">
      <h1 className="text-2xl font-bold">{title}</h1>
      {children}
    </div>
  )
}

// ErrorAlert shows error, and nothing when there is none.
export function ErrorAlert({
  error,
  className = 'mb-4',
}: {
  error: string | null
  className?: string
}) {
  if (!error) return null
  return (
    <div role="alert" className={`alert alert-error ${className}`}>
      <span>{error}</span>
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
