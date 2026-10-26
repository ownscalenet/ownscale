import { ExpiryDialog, RenameDialog, TagsDialog, RoutesDialog } from '../components/MachineDialogs'
import { useDialogAction } from '../hooks/useDialogAction'
import { RelativeTime } from '../components/RelativeTime'
import {
  BookOpen,
  Ellipsis,
  KeyRound,
  LogOut,
  Pencil,
  Plus,
  Route,
  Tag,
  TerminalSquare,
  Trash2,
} from 'lucide-react'
import { useEffect, useState } from 'react'
import { Menu, MenuButton, MenuItem, MenuItems } from '@headlessui/react'
import { keepPreviousData, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, useSearchParams } from 'react-router-dom'
import { useAuth } from '../authContext'
import { usePageTitle } from '../hooks/usePageTitle'
import { shortURL } from '../lib/faq'
import { useConfirm } from '../lib/confirm'
import Modal from '../components/Modal'
import { CopyIcon } from '../components/CopyButton'
import {
  deleteMachine,
  errorMessage,
  exitNodeStatus,
  expireMachine,
  listFunnelRoutes,
  listMachines,
  registerMachine,
  type AssignableUser,
  type Machine,
} from '../api'
import { ErrorAlert, ExitNodeBadge, PageHeader, StatusBadge } from '../components/ui'

// refreshIntervalMs is how often the Machines page reloads the list, so online
// status, last seen and expiry stay current.
const refreshIntervalMs = 10_000

// searchDelayMs is how long the search box waits for typing to stop before
// filtering, so a word costs one request instead of one per keystroke.
const searchDelayMs = 300

// isExpired uses the server's verdict on the expiry rather than comparing
// it with this browser's clock, which may be off.
function isExpired(m: Machine): boolean {
  return m.expired
}

function ipv4(m: Machine): string | undefined {
  return m.ipAddresses.find((ip) => !ip.includes(':'))
}

function tags(m: Machine): string[] {
  return m.tags ?? []
}

function statusLabel(m: Machine): string {
  if (isExpired(m)) return 'Expired'
  return m.online ? 'Connected' : 'Offline'
}

// csvField escapes a single CSV field per RFC 4180: wrap in quotes (doubling
// any internal quotes) whenever the value contains a comma, quote, or
// newline.
function csvField(value: string): string {
  if (/[",\n]/.test(value)) {
    return '"' + value.replace(/"/g, '""') + '"'
  }
  return value
}

function machinesToCsv(machines: Machine[]): string {
  const header = ['Name', 'IP addresses', 'Owner', 'Status', 'Tags', 'Last seen', 'Expiry']
  const rows = machines.map((m) => [
    m.givenName,
    m.ipAddresses.join('; '),
    m.user?.name ?? '',
    statusLabel(m),
    tags(m).join('; '),
    m.lastSeen ?? '',
    m.expiry ?? '',
  ])
  return [header, ...rows].map((row) => row.map(csvField).join(',')).join('\r\n') + '\r\n'
}

function downloadCsv(filename: string, csv: string) {
  const blob = new Blob([csv], { type: 'text/csv;charset=utf-8;' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = filename
  document.body.appendChild(a)
  a.click()
  a.remove()
  URL.revokeObjectURL(url)
}

export default function Machines() {
  usePageTitle('Machines')
  const [searchParams, setSearchParams] = useSearchParams()
  const q = searchParams.get('q') ?? ''
  const userFilter = searchParams.get('user') ?? ''
  const status = searchParams.get('status') ?? ''
  const filtered = q !== '' || userFilter !== '' || status !== ''

  const queryClient = useQueryClient()
  const filters = { q, user: userFilter, status }
  // Polling pauses while the tab is hidden. Dialogs keep their own copy of the
  // machine, so a refresh can't reset what's being edited.
  const query = useQuery({
    queryKey: ['machines', filters],
    queryFn: ({ signal }) => listMachines(filters, signal),
    placeholderData: keepPreviousData,
    refetchInterval: refreshIntervalMs,
    refetchOnWindowFocus: true,
  })
  const machines = query.data?.machines ?? []
  const users = query.data?.users ?? []
  const assignableUsers = query.data?.assignableUsers ?? []
  const total = query.data?.total ?? 0
  const canManage = query.data?.canManage ?? false
  // Without data the page has nothing to show but the error.
  const failed = query.data == null && query.error != null
  const [actionError, setActionError] = useState<string | null>(null)
  const error = actionError ?? (query.error ? errorMessage(query.error) : null)
  const reload = () => {
    setActionError(null)
    void queryClient.invalidateQueries({ queryKey: ['machines'] })
  }
  const [dialog, setDialog] = useState<MachineDialog | null>(null)
  const closeDialog = () => setDialog(null)

  // Filters replace the history entry, so Back leaves the page instead of
  // stepping through every filter change.
  const updateFilter = (key: string, value: string) => {
    setSearchParams(
      (prev) => {
        const next = new URLSearchParams(prev)
        if (value) next.set(key, value)
        else next.delete(key)
        return next
      },
      { replace: true },
    )
  }

  const [qInput, setQInput] = useState(q)
  // Follows the URL when it changes some other way, e.g. Clear or Back.
  useEffect(() => setQInput(q), [q])
  useEffect(() => {
    if (qInput === q) return
    const timer = setTimeout(() => updateFilter('q', qInput), searchDelayMs)
    return () => clearTimeout(timer)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [qInput])

  return (
    <>
      <PageHeader title="Machines">
        {!failed && (
          <button
            type="button"
            className="btn btn-sm btn-primary"
            onClick={() => setDialog({ kind: 'register' })}
          >
            <Plus className="size-3.5" aria-hidden="true" />
            Register machine
          </button>
        )}
      </PageHeader>

      {dialog?.kind === 'register' && (
        <RegisterMachineDialog users={assignableUsers} onClose={closeDialog} onDone={reload} />
      )}
      {dialog?.kind === 'rename' && (
        <RenameDialog machine={dialog.machine} onClose={closeDialog} onDone={reload} />
      )}
      {dialog?.kind === 'tags' && (
        <TagsDialog machine={dialog.machine} onClose={closeDialog} onDone={reload} />
      )}
      {dialog?.kind === 'routes' && (
        <RoutesDialog machine={dialog.machine} onClose={closeDialog} onDone={reload} />
      )}
      {dialog?.kind === 'expiry' && (
        <ExpiryDialog machine={dialog.machine} onClose={closeDialog} onDone={reload} />
      )}
      {dialog?.kind === 'removeInternal' && (
        <RemoveInternalDialog machine={dialog.machine} onClose={closeDialog} onDone={reload} />
      )}

      <ErrorAlert error={error} />

      {!failed && (
        <>
          <form className="flex flex-wrap gap-2 mb-4" onSubmit={(e) => e.preventDefault()}>
            <input
              type="text"
              placeholder="Search by name or IP…"
              className="input input-bordered input-sm w-64"
              value={qInput}
              onChange={(e) => setQInput(e.target.value)}
            />
            <select
              className="select select-bordered select-sm"
              value={userFilter}
              onChange={(e) => updateFilter('user', e.target.value)}
            >
              <option value="">All users</option>
              {users.map((u) => (
                <option key={u.id} value={u.name}>
                  {u.name}
                </option>
              ))}
            </select>
            <select
              className="select select-bordered select-sm"
              value={status}
              onChange={(e) => updateFilter('status', e.target.value)}
            >
              <option value="">Any status</option>
              <option value="online">Online</option>
              <option value="offline">Offline</option>
              <option value="expired">Expired</option>
            </select>
            {filtered && (
              <button
                type="button"
                className="btn btn-sm btn-ghost"
                onClick={() => setSearchParams({}, { replace: true })}
              >
                Clear
              </button>
            )}
            <button
              type="button"
              className="btn btn-sm btn-outline ml-auto"
              disabled={machines.length === 0}
              onClick={() => downloadCsv('machines.csv', machinesToCsv(machines))}
            >
              Download CSV
            </button>
          </form>

          {filtered && (
            <p className="text-sm opacity-60 mb-2">
              {machines.length} of {total} machines
            </p>
          )}

          <div className="card bg-base-100 shadow">
            <div className="card-body p-0">
              {machines.length > 0 ? (
                <div className="overflow-x-auto">
                  <table className="table">
                    <thead>
                      <tr>
                        <th className="whitespace-nowrap">Name</th>
                        <th className="whitespace-nowrap">Addresses</th>
                        <th className="whitespace-nowrap">Last seen</th>
                        <th className="text-right whitespace-nowrap">Actions</th>
                      </tr>
                    </thead>
                    <tbody>
                      {machines.map((m) => (
                        <MachineRow
                          key={m.id}
                          machine={m}
                          canManage={canManage}
                          canEdit={m.canEdit}
                          onChange={reload}
                          onError={setActionError}
                          onOpen={(kind) => setDialog({ kind, machine: m })}
                        />
                      ))}
                    </tbody>
                  </table>
                </div>
              ) : (
                <div className="p-8 text-center opacity-60">
                  {filtered
                    ? 'No machines match the current filters.'
                    : 'No machines registered yet.'}
                </div>
              )}
            </div>
          </div>
        </>
      )}
    </>
  )
}

// MachineDialog is the dialog the Machines page has open, if any.
type MachineDialog =
  | { kind: 'register' }
  | { kind: 'rename' | 'tags' | 'routes' | 'expiry' | 'removeInternal'; machine: Machine }

type MachineDialogKind = Exclude<MachineDialog['kind'], 'register'>

function RegisterMachineDialog({
  users,
  onClose,
  onDone,
}: {
  users: AssignableUser[]
  onClose: () => void
  onDone: () => void
}) {
  const [memberId, setMemberId] = useState('')
  const [key, setKey] = useState('')
  const [validation, setValidation] = useState<string | null>(null)
  const { organization, refresh } = useAuth()
  // The first registration starts the org's headscale, changing its status.
  const { busy, error, run } = useDialogAction(onClose, () => {
    onDone()
    void refresh()
  })
  const loginServer = `${window.location.protocol}//${organization?.Domain}`
  const command = `tailscale up --login-server=${loginServer}`

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault()
    setValidation(null)
    if (!memberId) {
      setValidation('Choose a user to own this machine.')
      return
    }
    if (!key.trim()) {
      setValidation('Paste the registration key or URL shown by `tailscale up`.')
      return
    }
    run(() => registerMachine(memberId, key.trim()))
  }

  return (
    <Modal open onClose={onClose} title="Register machine">
      <p className="text-sm opacity-60 mb-2">1. Run this on the device you want to add:</p>
      <div className="relative mb-4">
        <div className="mockup-code text-xs overflow-x-auto">
          <pre data-prefix="$">
            <code>{command}</code>
          </pre>
        </div>
        {/* The code block is neutral in every theme; the ghost button colors itself with base-content. */}
        <span className="absolute top-2 right-2 [--color-base-content:var(--color-neutral-content)]">
          <CopyIcon text={command} label="command" />
        </span>
      </div>
      <p className="text-sm opacity-60 mb-2">2. Follow the instructions from the device.</p>
      <div className="divider my-2">or</div>
      <p className="text-sm opacity-60 mb-2">2. Finish registration from this session.</p>
      <ErrorAlert error={validation ?? error} />
      <form className="space-y-4" onSubmit={handleSubmit}>
        <fieldset className="fieldset">
          <legend className="fieldset-legend">Registration key or URL</legend>
          <input
            id="register-machine-key"
            type="text"
            className="input input-bordered w-full font-mono"
            placeholder="hskey-authreq-… or the full registration URL"
            value={key}
            onChange={(e) => setKey(e.target.value)}
            required
          />
          <span className="opacity-60 mt-1 text-sm">
            Printed by the command above once the device is waiting to register.
          </span>
        </fieldset>
        <fieldset className="fieldset">
          <legend className="fieldset-legend">Owner</legend>
          <select
            id="register-machine-user"
            className="select select-bordered w-full"
            value={memberId}
            onChange={(e) => setMemberId(e.target.value)}
            required
          >
            <option value="" disabled>
              Select a user
            </option>
            {users.map((u) => (
              <option key={u.id} value={u.id}>
                {u.name || u.email}
              </option>
            ))}
          </select>
        </fieldset>
        <div className="modal-action">
          <button type="submit" className="btn btn-primary" disabled={busy}>
            Register
          </button>
          <button type="button" className="btn btn-ghost" onClick={onClose}>
            Cancel
          </button>
        </div>
      </form>
    </Modal>
  )
}

// RemoveInternalDialog removes a funnel's internal machine, listing the
// funnels that go with it.
function RemoveInternalDialog({
  machine: m,
  onClose,
  onDone,
}: {
  machine: Machine
  onClose: () => void
  onDone: () => void
}) {
  const funnelsQuery = useQuery({
    queryKey: ['funnel'],
    queryFn: ({ signal }) => listFunnelRoutes(signal),
  })
  const funnels = funnelsQuery.data?.funnelRoutes ?? null
  const funnelsError = funnelsQuery.error ? errorMessage(funnelsQuery.error) : null
  const { busy, error, run } = useDialogAction(onClose, onDone)

  return (
    <Modal open onClose={onClose} title={`Remove ${m.givenName}`} className="whitespace-normal">
        <span>
          This machine is used internally by the funnel feature. The following funnels will be
          deleted and no longer accessible:
        </span>
      {funnelsError ? (
        <ErrorAlert error={funnelsError} />
      ) : funnels === null ? (
        <div className="flex justify-center py-2">
          <span className="loading loading-spinner loading-sm"></span>
        </div>
      ) : funnels.length === 0 ? (
        <p className="text-sm opacity-60">No funnels are currently configured.</p>
      ) : (
        <ul className="list-disc list-inside space-y-1">
          {funnels.map((f) => (
            <li key={f.id} className="font-mono text-sm">
              {f.hostname}
            </li>
          ))}
        </ul>
      )}
      <ErrorAlert error={error} />
      <div className="modal-action">
        <button
          type="button"
          className="btn btn-error"
          disabled={busy || (funnels === null && !funnelsError)}
          onClick={() => run(() => deleteMachine(m.id))}
        >
          <Trash2 className="size-3.5" aria-hidden="true" />
          Delete machine and funnels
        </button>
        <button type="button" className="btn btn-ghost" onClick={onClose}>
          Cancel
        </button>
      </div>
    </Modal>
  )
}

// MachineRow renders one machine. canEdit allows renaming, expiring and
// removing it; tags and routes need canManage. Its dialogs are opened on the
// page through onOpen.
function MachineRow({
  machine: m,
  canManage,
  canEdit,
  onChange,
  onError,
  onOpen,
}: {
  machine: Machine
  canManage: boolean
  canEdit: boolean
  onChange: () => void
  onError: (message: string) => void
  onOpen: (kind: MachineDialogKind) => void
}) {
  const confirm = useConfirm()
  const action = useMutation({
    mutationFn: (fn: () => Promise<unknown>) => fn(),
    onSuccess: onChange,
    onError: (err) => onError(errorMessage(err)),
  })
  const busy = action.isPending
  const run = (fn: () => Promise<unknown>) => action.mutate(fn)
  const expired = isExpired(m)

  return (
    <tr>
      <td className="whitespace-nowrap">
        <div className="font-bold font-mono">
          <span className="inline-flex items-center gap-1">
            {ipv4(m) ? (
              <Link to={`/machines/${ipv4(m)}`} className="link link-hover">
                {m.givenName}
              </Link>
            ) : (
              m.givenName
            )}
            <CopyIcon text={m.givenName} label="machine name" />
          </span>
        </div>
        <div className="text-sm opacity-60">
          {m.user?.name ? (
            <span className="inline-flex items-center gap-1">
              {m.user.name}
              <CopyIcon text={m.user.name} label="user name" />
            </span>
          ) : (
            '—'
          )}
        </div>
        <div className="flex flex-wrap gap-1 mt-1">
          {expired && <StatusBadge status="expired" />}
          {!m.expiry && <span className="badge badge-ghost badge-sm">No expiry</span>}
          <ExitNodeBadge status={exitNodeStatus(m)} />
          {m.internal && (
            <a
              href={shortURL('agent')}
              target="_blank"
              rel="noreferrer"
              className="badge badge-info badge-sm gap-1"
              title="Automatically created for the funnel feature"
            >
              <BookOpen className="size-3.5" aria-hidden="true" />
              Agent
            </a>
          )}
          {tags(m).map((t) => (
            <span key={t} className="badge badge-outline badge-sm font-mono">
              {t}
            </span>
          ))}
        </div>
      </td>
      <td className="whitespace-nowrap">
        <div className="font-mono text-sm">
          {m.ipAddresses.map((ip) => (
            <div key={ip} className="flex items-center gap-1">
              {ip}
              <CopyIcon text={ip} label="IP address" />
            </div>
          ))}
        </div>
      </td>
      <td className="whitespace-nowrap">
        {m.online && !expired ? (
          <span className="inline-flex items-center gap-2">
            <span className="w-2 h-2 rounded-full bg-success inline-block"></span> Connected
          </span>
        ) : m.lastSeen ? (
          <RelativeTime value={m.lastSeen} />
        ) : (
          <span className="opacity-60">Never</span>
        )}
      </td>
      <td className="text-right whitespace-nowrap">
        {/* Headless UI renders the items in a portal next to the button, so the
            table's scroll container can't clip them. */}
        <Menu>
          <MenuButton className="btn btn-sm btn-ghost btn-square" aria-label="Actions">
            <Ellipsis className="size-4" aria-hidden="true" />
          </MenuButton>
          <MenuItems
            as="ul"
            anchor="bottom end"
            className="menu bg-base-100 rounded-box z-10 w-56 p-2 shadow [--anchor-gap:4px] focus:outline-none"
          >
            <li>
              <MenuItem disabled={m.internal || !m.online || expired}>
                <button
                  type="button"
                  className="data-focus:bg-base-content/10"
                  disabled={m.internal || !m.online || expired}
                  // A full page load, not client-side routing: the SSH page
                  // needs its own Content-Security-Policy to run the
                  // Tailscale WebAssembly client (see server.sshCSP).
                  onClick={() => {
                    window.location.href = `/machines/${m.id}/ssh`
                  }}
                >
                  <TerminalSquare className="size-3.5" aria-hidden="true" />
                  SSH
                </button>
              </MenuItem>
            </li>
            <li>
              <MenuItem disabled={m.internal || !canEdit}>
                <button
                  type="button"
                  className="data-focus:bg-base-content/10"
                  disabled={m.internal || !canEdit}
                  onClick={() => onOpen('rename')}
                >
                  <Pencil className="size-3.5" aria-hidden="true" />
                  Edit machine name
                </button>
              </MenuItem>
            </li>
            <li>
              <MenuItem disabled={m.internal || !canManage}>
                <button
                  type="button"
                  className="data-focus:bg-base-content/10"
                  disabled={m.internal || !canManage}
                  onClick={() => onOpen('tags')}
                >
                  <Tag className="size-3.5" aria-hidden="true" />
                  Edit ACL tags
                </button>
              </MenuItem>
            </li>
            <li>
              <MenuItem disabled={m.internal || !canManage}>
                <button
                  type="button"
                  className="data-focus:bg-base-content/10"
                  disabled={m.internal || !canManage}
                  onClick={() => onOpen('routes')}
                >
                  <Route className="size-3.5" aria-hidden="true" />
                  Edit route settings
                </button>
              </MenuItem>
            </li>
            <li>
              <MenuItem disabled={m.internal || !canEdit}>
                <button
                  type="button"
                  className="data-focus:bg-base-content/10"
                  disabled={m.internal || !canEdit}
                  onClick={() => onOpen('expiry')}
                >
                  <KeyRound className="size-3.5" aria-hidden="true" />
                  {m.expiry ? 'Disable key expiry' : 'Enable key expiry'}
                </button>
              </MenuItem>
            </li>
            <li className="border-t border-base-200 mt-1 pt-1">
              <MenuItem disabled={expired || busy || m.internal || !canEdit}>
                <button
                  type="button"
                  className="text-error data-focus:bg-base-content/10"
                  disabled={expired || busy || m.internal || !canEdit}
                  onClick={async () => {
                    const ok = await confirm({
                      title: `Expire the session of ${m.givenName}?`,
                      consequences: [
                        'It is disconnected now and has to log in again to reconnect.',
                        'Nothing is deleted.',
                      ],
                      confirmLabel: 'Expire session',
                      tone: 'danger',
                    })
                    if (ok) run(() => expireMachine(m.id))
                  }}
                >
                  <LogOut className="size-3.5" aria-hidden="true" />
                  Expire session
                </button>
              </MenuItem>
            </li>
            <li>
              <MenuItem disabled={busy || !canEdit}>
                <button
                  type="button"
                  className="text-error data-focus:bg-base-content/10"
                  disabled={busy || !canEdit}
                  onClick={async () => {
                    if (m.internal) {
                      onOpen('removeInternal')
                      return
                    }
                    const ok = await confirm({
                      title: `Permanently remove ${m.givenName}?`,
                      consequences: [
                        `${m.givenName} will not be accesible anymore on the tailnet.`,
                      ],
                      confirmLabel: 'Remove',
                      tone: 'danger',
                    })
                    if (ok) run(() => deleteMachine(m.id))
                  }}
                >
                  <Trash2 className="size-3.5" aria-hidden="true" />
                  Remove
                </button>
              </MenuItem>
            </li>
          </MenuItems>
        </Menu>
      </td>
    </tr>
  )
}
