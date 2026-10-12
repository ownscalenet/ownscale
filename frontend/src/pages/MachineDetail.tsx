import type { ReactNode } from 'react'
import { useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Pencil, Route, Tag, TerminalSquare } from 'lucide-react'
import { ApiError, errorMessage, getMachineByIP, type Machine } from '../api'
import { usePageTitle } from '../hooks/usePageTitle'
import { CopyIcon } from '../components/CopyButton'
import { RenameDialog, RoutesDialog, TagsDialog } from '../components/MachineDialogs'
import { RelativeTime } from '../components/RelativeTime'
import { ErrorAlert, PageSpinner, StatusBadge } from '../components/ui'

const refreshIntervalMs = 10_000

// Attribute is one labelled value of a details card; copyable values get a
// copy button, and a missing value shows as a dash.
function Attribute({
  name,
  value,
  copyable,
  mono,
}: {
  name: string
  value?: ReactNode
  copyable?: boolean
  mono?: boolean
}) {
  const empty = value === undefined || value === null || value === ''
  return (
    <div className="py-1.5">
      <dt className="text-sm text-base-content/60">{name}</dt>
      <dd className={`flex items-center gap-1 break-all ${mono ? 'font-mono text-sm' : ''}`}>
        {empty ? <span className="opacity-50">—</span> : value}
        {copyable && !empty && typeof value === 'string' && <CopyIcon text={value} label={name} />}
      </dd>
    </div>
  )
}

function formatDate(value: string | null): string | undefined {
  return value ? new Date(value).toLocaleString() : undefined
}

function RouteList({ routes }: { routes: string[] }) {
  if (routes.length === 0) return <span className="opacity-50">—</span>
  return (
    <ul className="font-mono text-sm leading-normal">
      {routes.map((r) => (
        <li key={r}>{r}</li>
      ))}
    </ul>
  )
}

// isExitRoute reports whether route is one of the two default routes a
// machine advertises to act as an exit node.
function isExitRoute(route: string): boolean {
  return route === '0.0.0.0/0' || route === '::/0'
}

export default function MachineDetail() {
  const { ip = '' } = useParams()
  usePageTitle(ip)
  const queryClient = useQueryClient()
  const query = useQuery({
    queryKey: ['machine', ip],
    queryFn: ({ signal }) => getMachineByIP(ip, signal),
    refetchInterval: refreshIntervalMs,
    retry: (count, err) => !(err instanceof ApiError && err.status === 404) && count < 3,
  })
  const [dialog, setDialog] = useState<'rename' | 'tags' | 'routes' | null>(null)
  const reload = () => queryClient.invalidateQueries({ queryKey: ['machine', ip] })

  if (query.isPending) return <PageSpinner />

  const breadcrumb = (
    <div className="breadcrumbs text-sm">
      <ul>
        <li>
          <Link to="/machines">All machines</Link>
        </li>
        <li>{ip}</li>
      </ul>
    </div>
  )

  if (query.error || !query.data) {
    const notFound = query.error instanceof ApiError && query.error.status === 404
    return (
      <div>
        {breadcrumb}
        <ErrorAlert error={notFound ? 'Machine not found' : errorMessage(query.error)} />
      </div>
    )
  }

  const { machine: m, deviceInfo, fullDomain } = query.data
  return (
    <div>
      {breadcrumb}
      <Header machine={m} onOpen={setDialog} />
      <MachineDetails machine={m} deviceInfo={deviceInfo} />
      <Addresses machine={m} fullDomain={fullDomain} />
      <Subnets machine={m} />

      {dialog === 'rename' && (
        <RenameDialog machine={m} onClose={() => setDialog(null)} onDone={reload} />
      )}
      {dialog === 'tags' && (
        <TagsDialog machine={m} onClose={() => setDialog(null)} onDone={reload} />
      )}
      {dialog === 'routes' && (
        <RoutesDialog machine={m} onClose={() => setDialog(null)} onDone={reload} />
      )}
    </div>
  )
}

function Header({
  machine: m,
  onOpen,
}: {
  machine: Machine
  onOpen: (kind: 'rename' | 'tags' | 'routes') => void
}) {
  const status = m.expired ? 'expired' : m.online ? 'online' : 'offline'
  return (
    <div className="mb-5">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="min-w-0">
          <h1 className="text-[22px] font-semibold tracking-tight font-mono">{m.givenName}</h1>
          <div className="flex flex-wrap items-center gap-2 mt-1">
            <StatusBadge status={status} />
            {m.internal && <span className="badge badge-info badge-sm">Internal</span>}
            {!m.expiry && <span className="badge badge-ghost badge-sm">No expiry</span>}
            {(m.tags ?? []).map((t) => (
              <span key={t} className="badge badge-outline badge-sm font-mono">
                {t}
              </span>
            ))}
          </div>
        </div>
        <div className="flex flex-wrap gap-2">
          {/* Plain anchor: the SSH page needs a full load for its own CSP. */}
          {m.internal || !m.online || m.expired ? (
            <button type="button" className="btn btn-sm" disabled>
              <TerminalSquare className="size-3.5" aria-hidden="true" />
              SSH
            </button>
          ) : (
            <a className="btn btn-sm" href={`/machines/${m.id}/ssh`}>
              <TerminalSquare className="size-3.5" aria-hidden="true" />
              SSH
            </a>
          )}
          <button
            type="button"
            className="btn btn-sm"
            disabled={m.internal || !m.canEdit}
            onClick={() => onOpen('rename')}
          >
            <Pencil className="size-3.5" aria-hidden="true" />
            Edit machine name
          </button>
          <button
            type="button"
            className="btn btn-sm"
            disabled={m.internal || !m.canEdit}
            onClick={() => onOpen('tags')}
          >
            <Tag className="size-3.5" aria-hidden="true" />
            Edit ACL tags
          </button>
          <button
            type="button"
            className="btn btn-sm"
            disabled={m.internal || !m.canEdit}
            onClick={() => onOpen('routes')}
          >
            <Route className="size-3.5" aria-hidden="true" />
            Edit route settings
          </button>
        </div>
      </div>
      <dl className="flex flex-wrap gap-x-10 mt-3">
        <Attribute name="Managed by" value={m.user?.name} />
        <Attribute
          name="Status"
          value={
            m.expired ? (
              <>Expired {formatDate(m.expiry)}</>
            ) : m.online ? (
              'Connected'
            ) : m.lastSeen ? (
              <RelativeTime value={m.lastSeen} />
            ) : (
              'Never connected'
            )
          }
        />
      </dl>
    </div>
  )
}

function Card({
  title,
  subtitle,
  children,
}: {
  title: string
  subtitle?: string
  children: ReactNode
}) {
  return (
    <div className="card bg-base-100 shadow mb-6">
      <div className="card-body">
        <h2 className="card-title">{title}</h2>
        {subtitle && <p className="text-sm text-base-content/60">{subtitle}</p>}
        {children}
      </div>
    </div>
  )
}

function MachineDetails({
  machine: m,
  deviceInfo,
}: {
  machine: Machine
  deviceInfo: { os: string; clientVersion: string; hostname: string; updateAvailable: boolean }
}) {
  return (
    <Card
      title="Machine details"
      subtitle="Information about this machine’s network. Used to debug connection issues."
    >
      <dl className="grid grid-cols-1 lg:grid-cols-2 gap-x-12">
        <div>
          <Attribute name="Creator" value={m.user?.name} />
          <Attribute name="Machine name" value={m.givenName} copyable />
          <Attribute name="OS hostname" value={deviceInfo.hostname || m.name} copyable />
          <Attribute name="OS" value={deviceInfo.os} />
          <Attribute
            name="Tailscale version"
            value={
              deviceInfo.clientVersion && (
                <>
                  {deviceInfo.clientVersion}
                  {deviceInfo.updateAvailable && (
                    <span className="badge badge-warning badge-sm ml-1">Update available</span>
                  )}
                </>
              )
            }
          />
          <Attribute name="ID" value={m.id} copyable mono />
        </div>
        <div>
          <Attribute name="Node key" value={m.nodeKey} copyable mono />
          <Attribute name="Created" value={formatDate(m.createdAt)} />
          <Attribute
            name="Last seen"
            value={
              m.online && !m.expired ? (
                'Connected'
              ) : m.lastSeen ? (
                <RelativeTime value={m.lastSeen} />
              ) : undefined
            }
          />
          <Attribute name="Key expiry" value={m.expiry ? formatDate(m.expiry) : 'No expiry'} />
        </div>
      </dl>
    </Card>
  )
}

function Addresses({ machine: m, fullDomain }: { machine: Machine; fullDomain: string }) {
  const v4 = m.ipAddresses.find((a) => !a.includes(':'))
  const v6 = m.ipAddresses.find((a) => a.includes(':'))
  return (
    <Card title="Addresses">
      <dl className="grid grid-cols-1 lg:grid-cols-2 gap-x-12">
        <Attribute name="Tailscale IPv4" value={v4} copyable mono />
        <Attribute name="Tailscale IPv6" value={v6} copyable mono />
        <Attribute name="Short domain" value={m.givenName} copyable mono />
        <Attribute name="Full domain" value={fullDomain} copyable mono />
      </dl>
    </Card>
  )
}

function Subnets({ machine: m }: { machine: Machine }) {
  const available = m.availableRoutes ?? []
  const approved = m.approvedRoutes ?? []
  const subnets = (routes: string[]) => routes.filter((r) => !isExitRoute(r))
  const awaiting = subnets(available.filter((r) => !approved.includes(r)))
  const advertisesExit = available.some(isExitRoute)
  const exitApproved = approved.some(isExitRoute)
  return (
    <Card
      title="Subnets and routing"
      subtitle="Subnets expose physical network routes onto the tailnet."
    >
      <div className="grid grid-cols-1 lg:grid-cols-3 gap-6">
        <div>
          <div className="text-sm text-base-content/60 mb-1">Approved routes</div>
          <RouteList routes={subnets(approved)} />
        </div>
        <div>
          <div className="text-sm text-base-content/60 mb-1">Awaiting approval</div>
          <RouteList routes={awaiting} />
        </div>
        <div>
          <div className="text-sm text-base-content/60 mb-1">Exit node</div>
          {!advertisesExit ? (
            <span className="opacity-50">—</span>
          ) : exitApproved ? (
            'Allowed'
          ) : (
            'Awaiting approval'
          )}
        </div>
      </div>
    </Card>
  )
}
