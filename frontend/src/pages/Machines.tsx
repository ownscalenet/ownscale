import { useEffect, useState } from 'react'
import { Menu, MenuButton, MenuItem, MenuItems } from '@headlessui/react'
import { keepPreviousData, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useSearchParams } from 'react-router-dom'
import { useAuth } from '../auth'
import { usePageTitle } from '../hooks/usePageTitle'
import { faqURL } from '../lib/faq'
import { useConfirm } from '../lib/confirm'
import Modal from '../components/Modal'
import { CopyIcon } from '../components/CopyButton'
import {
  deleteMachine,
  errorMessage,
  expireMachine,
  isNotConfigured,
  listFunnelRoutes,
  listMachines,
  registerMachine,
  renameMachine,
  setMachineRoutes,
  setMachineTags,
  type AssignableUser,
  type Machine,
} from '../api'
import { ErrorAlert, PageHeader } from '../components/ui'

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
  const notConfigured = isNotConfigured(query.error)
  const [actionError, setActionError] = useState<string | null>(null)
  const error = actionError ?? (query.error && !notConfigured ? errorMessage(query.error) : null)
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
        {!notConfigured && (
          <button
            type="button"
            className="btn btn-sm btn-primary"
            onClick={() => setDialog({ kind: 'register' })}
          >
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
      {dialog?.kind === 'removeInternal' && (
        <RemoveInternalDialog machine={dialog.machine} onClose={closeDialog} onDone={reload} />
      )}

      <ErrorAlert error={error} />

      {notConfigured ? (
        <div className="alert alert-warning">
          <span>
            Headscale isn&rsquo;t reachable for this organization right now. This happens briefly
            while it restarts to apply saved settings &mdash; try again in a moment.
          </span>
        </div>
      ) : (
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
  { kind: 'register' } | { kind: 'rename' | 'tags' | 'routes' | 'removeInternal'; machine: Machine }

type MachineDialogKind = Exclude<MachineDialog['kind'], 'register'>

// useDialogAction runs a dialog's action: busy while it runs, its error shown
// in the dialog, and on success onDone (reload the list) and onClose.
function useDialogAction(onClose: () => void, onDone: () => void) {
  const mutation = useMutation({
    mutationFn: (action: () => Promise<unknown>) => action(),
    onSuccess: () => {
      onDone()
      onClose()
    },
  })
  return {
    busy: mutation.isPending,
    error: mutation.error ? errorMessage(mutation.error) : null,
    run: (action: () => Promise<unknown>) => mutation.mutate(action),
  }
}

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
      <p className="text-sm opacity-60 mb-2">Run this on the device you want to add:</p>
      <div className="mockup-code text-xs mb-4 overflow-x-auto">
        <pre data-prefix="$">
          <code>tailscale up --login-server={loginServer}</code>
        </pre>
      </div>
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

function RenameDialog({
  machine: m,
  onClose,
  onDone,
}: {
  machine: Machine
  onClose: () => void
  onDone: () => void
}) {
  const [name, setName] = useState(m.givenName)
  const { busy, error, run } = useDialogAction(onClose, onDone)
  return (
    <Modal open onClose={onClose} title={`Rename ${m.givenName}`}>
      <ErrorAlert error={error} />
      <form
        className="space-y-4"
        onSubmit={(e) => {
          e.preventDefault()
          run(() => renameMachine(m.id, name))
        }}
      >
        <input
          type="text"
          className="input input-bordered w-full"
          value={name}
          onChange={(e) => setName(e.target.value)}
          required
        />
        <div className="modal-action">
          <button type="submit" className="btn btn-primary" disabled={busy}>
            Rename
          </button>
          <button type="button" className="btn btn-ghost" onClick={onClose}>
            Cancel
          </button>
        </div>
      </form>
    </Modal>
  )
}

function TagsDialog({
  machine: m,
  onClose,
  onDone,
}: {
  machine: Machine
  onClose: () => void
  onDone: () => void
}) {
  const [tagsInput, setTagsInput] = useState(tags(m).join(', '))
  const { busy, error, run } = useDialogAction(onClose, onDone)
  return (
    <Modal open onClose={onClose} title={`Edit ACL tags of ${m.givenName}`}>
      <ErrorAlert error={error} />
      <form
        className="space-y-4"
        onSubmit={(e) => {
          e.preventDefault()
          const parsed = tagsInput
            .split(',')
            .map((t) => t.trim())
            .filter(Boolean)
            .map((t) => (t.startsWith('tag:') ? t : `tag:${t}`))
          run(() => setMachineTags(m.id, parsed))
        }}
      >
        <input
          type="text"
          className="input input-bordered w-full font-mono"
          placeholder="tag:server, tag:prod"
          value={tagsInput}
          onChange={(e) => setTagsInput(e.target.value)}
        />
        <p className="text-sm opacity-60">
          Comma-separated. The <code>tag:</code> prefix is added automatically.
        </p>
        <div className="modal-action">
          <button type="submit" className="btn btn-primary" disabled={busy}>
            Save
          </button>
          <button type="button" className="btn btn-ghost" onClick={onClose}>
            Cancel
          </button>
        </div>
      </form>
    </Modal>
  )
}

function RoutesDialog({
  machine: m,
  onClose,
  onDone,
}: {
  machine: Machine
  onClose: () => void
  onDone: () => void
}) {
  const [selectedRoutes, setSelectedRoutes] = useState<string[]>(m.approvedRoutes ?? [])
  const { busy, error, run } = useDialogAction(onClose, onDone)
  const availableRoutes = m.availableRoutes ?? []
  return (
    <Modal open onClose={onClose} title={`Edit route settings of ${m.givenName}`}>
      <ErrorAlert error={error} />
      <form
        className="space-y-4"
        onSubmit={(e) => {
          e.preventDefault()
          run(() => setMachineRoutes(m.id, selectedRoutes))
        }}
      >
        {availableRoutes.length > 0 ? (
          <ul className="space-y-2">
            {availableRoutes.map((route) => (
              <li key={route}>
                <label className="fieldset-label cursor-pointer gap-3">
                  <input
                    type="checkbox"
                    className="checkbox checkbox-sm"
                    checked={selectedRoutes.includes(route)}
                    onChange={(e) => {
                      setSelectedRoutes((prev) =>
                        e.target.checked ? [...prev, route] : prev.filter((r) => r !== route),
                      )
                    }}
                  />
                  <span className="font-mono text-sm">{route}</span>
                </label>
              </li>
            ))}
          </ul>
        ) : (
          <p className="text-sm opacity-60">This machine hasn&rsquo;t advertised any routes.</p>
        )}
        <div className="modal-action">
          <button
            type="submit"
            className="btn btn-primary"
            disabled={busy || availableRoutes.length === 0}
          >
            Save
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
      <div role="alert" className="alert alert-warning mb-4">
        <span>
          This machine is used internally by the funnel feature. The following funnels will be
          deleted and no longer accessible:
        </span>
      </div>
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
        <div className="font-bold">
          <span className="inline-flex items-center gap-1">
            {m.givenName}
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
          {expired && <span className="badge badge-error badge-sm">Expired</span>}
          {!m.expiry && <span className="badge badge-ghost badge-sm">No expiry</span>}
          {m.internal && (
            <a
              href={faqURL('funnel-machine')}
              target="_blank"
              rel="noreferrer"
              className="badge badge-info badge-sm"
              title="Automatically created for the funnel feature"
            >
              Internal
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
          new Date(m.lastSeen).toLocaleString()
        ) : (
          <span className="opacity-60">Never</span>
        )}
      </td>
      <td className="text-right whitespace-nowrap">
        {/* Headless UI renders the items in a portal next to the button, so the
            table's scroll container can't clip them. */}
        <Menu>
          <MenuButton className="btn btn-sm btn-ghost">⋯</MenuButton>
          <MenuItems
            as="ul"
            anchor="bottom end"
            className="menu bg-base-100 rounded-box z-10 w-56 p-2 shadow [--anchor-gap:4px] focus:outline-none"
          >
            <li>
              <MenuItem disabled={m.internal || !canEdit}>
                <button
                  type="button"
                  className="data-focus:bg-base-content/10"
                  disabled={m.internal || !canEdit}
                  onClick={() => onOpen('rename')}
                >
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
                  Edit route settings
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
                      message: 'It will have to log in again to reconnect.',
                      confirmLabel: 'Expire session',
                      danger: true,
                    })
                    if (ok) run(() => expireMachine(m.id))
                  }}
                >
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
                      message: 'It will be removed from the tailnet.',
                      confirmLabel: 'Remove',
                      danger: true,
                    })
                    if (ok) run(() => deleteMachine(m.id))
                  }}
                >
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
