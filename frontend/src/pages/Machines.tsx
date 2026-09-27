import { useEffect, useRef, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { useAuth } from '../auth'
import { usePageTitle } from '../hooks/usePageTitle'
import { faqURL } from '../lib/faq'
import {
  ApiError,
  deleteMachine,
  expireMachine,
  isAbortError,
  listFunnelRoutes,
  listMachines,
  registerMachine,
  renameMachine,
  setMachineRoutes,
  setMachineTags,
  type FunnelRoute,
  type HeadscaleUser,
  type Machine,
} from '../api'

function isExpired(m: Machine): boolean {
  return m.expiry != null && new Date(m.expiry).getTime() < Date.now()
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

  const [machines, setMachines] = useState<Machine[]>([])
  const [users, setUsers] = useState<HeadscaleUser[]>([])
  const [total, setTotal] = useState(0)
  const [notConfigured, setNotConfigured] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const registerRef = useRef<HTMLDialogElement>(null)

  const load = async (signal?: AbortSignal) => {
    try {
      const res = await listMachines({ q, user: userFilter, status }, signal)
      setMachines(res.machines ?? [])
      setUsers(res.users ?? [])
      setTotal(res.total)
      setNotConfigured(false)
      setError(null)
    } catch (err) {
      if (isAbortError(err)) return
      if (err instanceof ApiError && err.status === 409) {
        setNotConfigured(true)
        setError(null)
        return
      }
      setError(err instanceof Error ? err.message : String(err))
    }
  }

  useEffect(() => {
    const controller = new AbortController()
    void load(controller.signal)
    return () => controller.abort()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [q, userFilter, status])

  const updateFilter = (key: string, value: string) => {
    const next = new URLSearchParams(searchParams)
    if (value) next.set(key, value)
    else next.delete(key)
    setSearchParams(next)
  }

  return (
    <>
      <div className="flex items-center justify-between mb-4">
        <h1 className="text-2xl font-bold">Machines</h1>
        {!notConfigured && (
          <button
            type="button"
            className="btn btn-sm btn-primary"
            onClick={() => registerRef.current?.showModal()}
          >
            Register machine
          </button>
        )}
      </div>

      <RegisterMachineDialog dialogRef={registerRef} users={users} onRegistered={load} />

      {error && (
        <div className="alert alert-error mb-4">
          <span>{error}</span>
        </div>
      )}

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
              value={q}
              onChange={(e) => updateFilter('q', e.target.value)}
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
                onClick={() => setSearchParams({})}
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
                        <MachineRow key={m.id} machine={m} onChange={load} />
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

function RegisterMachineDialog({
  dialogRef,
  users,
  onRegistered,
}: {
  dialogRef: React.RefObject<HTMLDialogElement | null>
  users: HeadscaleUser[]
  onRegistered: () => void
}) {
  const [userId, setUserId] = useState('')
  const [key, setKey] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const { organization } = useAuth()
  const loginServer = `${window.location.protocol}//${organization?.Domain}`

  const reset = () => {
    setUserId('')
    setKey('')
    setError(null)
  }

  const close = () => {
    dialogRef.current?.close()
    reset()
  }

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    setError(null)
    if (!userId) {
      setError('Choose a user to own this machine.')
      return
    }
    if (!key.trim()) {
      setError('Paste the registration key or URL shown by `tailscale up`.')
      return
    }
    setBusy(true)
    try {
      await registerMachine(userId, key.trim())
      onRegistered()
      close()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <dialog ref={dialogRef} className="modal">
      <div className="modal-box text-left">
        <h3 className="text-lg font-bold mb-4">Register machine</h3>
        <p className="text-sm opacity-60 mb-2">Run this on the device you want to add:</p>
        <div className="mockup-code text-xs mb-4 overflow-x-auto">
          <pre data-prefix="$">
            <code>tailscale up --login-server={loginServer}</code>
          </pre>
        </div>
        {error && (
          <div className="alert alert-error mb-4">
            <span>{error}</span>
          </div>
        )}
        <form className="space-y-4" onSubmit={(e) => void handleSubmit(e)}>
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
              value={userId}
              onChange={(e) => setUserId(e.target.value)}
              required
            >
              <option value="" disabled>
                Select a user
              </option>
              {users.map((u) => (
                <option key={u.id} value={u.name}>
                  {u.name}
                </option>
              ))}
            </select>
          </fieldset>
          <div className="modal-action">
            <button type="submit" className="btn btn-primary" disabled={busy}>
              Register
            </button>
            <button type="button" className="btn btn-ghost" onClick={close}>
              Cancel
            </button>
          </div>
        </form>
      </div>
      <form method="dialog" className="modal-backdrop">
        <button>close</button>
      </form>
    </dialog>
  )
}

function MachineRow({ machine: m, onChange }: { machine: Machine; onChange: () => void }) {
  const renameRef = useRef<HTMLDialogElement>(null)
  const tagsRef = useRef<HTMLDialogElement>(null)
  const routesRef = useRef<HTMLDialogElement>(null)
  const removeInternalRef = useRef<HTMLDialogElement>(null)
  const [name, setName] = useState(m.givenName)
  const [tagsInput, setTagsInput] = useState(tags(m).join(', '))
  const [selectedRoutes, setSelectedRoutes] = useState<string[]>(m.approvedRoutes ?? [])
  const [busy, setBusy] = useState(false)
  const [affectedFunnels, setAffectedFunnels] = useState<FunnelRoute[]>([])
  const [affectedFunnelsLoading, setAffectedFunnelsLoading] = useState(false)
  const [affectedFunnelsError, setAffectedFunnelsError] = useState<string | null>(null)
  const expired = isExpired(m)
  const availableRoutes = m.availableRoutes ?? []

  const run = async (fn: () => Promise<unknown>) => {
    setBusy(true)
    try {
      await fn()
      onChange()
    } finally {
      setBusy(false)
    }
  }

  const openRemoveInternalDialog = () => {
    removeInternalRef.current?.showModal()
    setAffectedFunnelsLoading(true)
    setAffectedFunnelsError(null)
    listFunnelRoutes()
      .then((res) => setAffectedFunnels(res.funnelRoutes))
      .catch((err) => {
        setAffectedFunnelsError(err instanceof Error ? err.message : String(err))
      })
      .finally(() => setAffectedFunnelsLoading(false))
  }

  return (
    <tr>
      <td className="whitespace-nowrap">
        <div className="font-bold">{m.givenName}</div>
        <div className="text-sm opacity-60">{m.user?.name ?? '—'}</div>
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
            <div key={ip}>{ip}</div>
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
        <div className="dropdown dropdown-end">
          <button tabIndex={0} className="btn btn-sm btn-ghost">
            ⋯
          </button>
          <ul
            tabIndex={0}
            className="dropdown-content menu bg-base-100 rounded-box z-10 w-56 p-2 shadow"
          >
            <li>
              <button
                type="button"
                disabled={m.internal}
                onClick={() => renameRef.current?.showModal()}
              >
                Edit machine name
              </button>
            </li>
            <li>
              <button
                type="button"
                disabled={m.internal}
                onClick={() => tagsRef.current?.showModal()}
              >
                Edit ACL tags
              </button>
            </li>
            <li>
              <button
                type="button"
                disabled={m.internal}
                onClick={() => routesRef.current?.showModal()}
              >
                Edit route settings
              </button>
            </li>
            <li className="border-t border-base-200 mt-1 pt-1">
              <button
                type="button"
                className="text-error"
                disabled={expired || busy || m.internal}
                onClick={() => {
                  if (confirm(`Expire the session of ${m.givenName}?`)) {
                    void run(() => expireMachine(m.id))
                  }
                }}
              >
                Expire session
              </button>
            </li>
            <li>
              <button
                type="button"
                className="text-error"
                disabled={busy}
                onClick={() => {
                  if (m.internal) {
                    openRemoveInternalDialog()
                    return
                  }
                  if (confirm(`Permanently remove ${m.givenName}?`)) {
                    void run(() => deleteMachine(m.id))
                  }
                }}
              >
                Remove
              </button>
            </li>
          </ul>
        </div>

        <dialog ref={renameRef} className="modal">
          <div className="modal-box text-left">
            <h3 className="text-lg font-bold mb-4">Rename {m.givenName}</h3>
            <form
              className="space-y-4"
              onSubmit={(e) => {
                e.preventDefault()
                void run(() => renameMachine(m.id, name)).then(() => renameRef.current?.close())
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
                <button
                  type="button"
                  className="btn btn-ghost"
                  onClick={() => renameRef.current?.close()}
                >
                  Cancel
                </button>
              </div>
            </form>
          </div>
          <form method="dialog" className="modal-backdrop">
            <button>close</button>
          </form>
        </dialog>

        <dialog ref={tagsRef} className="modal">
          <div className="modal-box text-left">
            <h3 className="text-lg font-bold mb-4">Edit ACL tags of {m.givenName}</h3>
            <form
              className="space-y-4"
              onSubmit={(e) => {
                e.preventDefault()
                const parsed = tagsInput
                  .split(',')
                  .map((t) => t.trim())
                  .filter(Boolean)
                  .map((t) => (t.startsWith('tag:') ? t : `tag:${t}`))
                void run(() => setMachineTags(m.id, parsed)).then(() => tagsRef.current?.close())
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
                <button
                  type="button"
                  className="btn btn-ghost"
                  onClick={() => tagsRef.current?.close()}
                >
                  Cancel
                </button>
              </div>
            </form>
          </div>
          <form method="dialog" className="modal-backdrop">
            <button>close</button>
          </form>
        </dialog>

        <dialog ref={routesRef} className="modal">
          <div className="modal-box text-left">
            <h3 className="text-lg font-bold mb-4">Edit route settings of {m.givenName}</h3>
            <form
              className="space-y-4"
              onSubmit={(e) => {
                e.preventDefault()
                void run(() => setMachineRoutes(m.id, selectedRoutes)).then(() =>
                  routesRef.current?.close(),
                )
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
                <p className="text-sm opacity-60">
                  This machine hasn&rsquo;t advertised any routes.
                </p>
              )}
              <div className="modal-action">
                <button
                  type="submit"
                  className="btn btn-primary"
                  disabled={busy || availableRoutes.length === 0}
                >
                  Save
                </button>
                <button
                  type="button"
                  className="btn btn-ghost"
                  onClick={() => routesRef.current?.close()}
                >
                  Cancel
                </button>
              </div>
            </form>
          </div>
          <form method="dialog" className="modal-backdrop">
            <button>close</button>
          </form>
        </dialog>

        <dialog ref={removeInternalRef} className="modal">
          <div className="modal-box text-left whitespace-normal">
            <h3 className="text-lg font-bold mb-4">Remove {m.givenName}</h3>
            <div role="alert" className="alert alert-warning mb-4">
              <span>
                This machine is used internally by the funnel feature. The following funnels will
                be deleted and no longer accessible:
              </span>
            </div>
            {affectedFunnelsLoading ? (
              <div className="flex justify-center py-2">
                <span className="loading loading-spinner loading-sm"></span>
              </div>
            ) : affectedFunnelsError ? (
              <div role="alert" className="alert alert-error mb-2">
                <span>{affectedFunnelsError}</span>
              </div>
            ) : affectedFunnels.length === 0 ? (
              <p className="text-sm opacity-60">No funnels are currently configured.</p>
            ) : (
              <ul className="list-disc list-inside space-y-1">
                {affectedFunnels.map((f) => (
                  <li key={f.id} className="font-mono text-sm">
                    {f.hostname}
                  </li>
                ))}
              </ul>
            )}
            <div className="modal-action">
              <button
                type="button"
                className="btn btn-error"
                disabled={busy || affectedFunnelsLoading}
                onClick={() => {
                  void run(() => deleteMachine(m.id)).then(() => removeInternalRef.current?.close())
                }}
              >
                Delete machine and funnels
              </button>
              <button
                type="button"
                className="btn btn-ghost"
                onClick={() => removeInternalRef.current?.close()}
              >
                Cancel
              </button>
            </div>
          </div>
          <form method="dialog" className="modal-backdrop">
            <button>close</button>
          </form>
        </dialog>
      </td>
    </tr>
  )
}
