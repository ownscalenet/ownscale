import { useEffect, useRef, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { useAuth } from '../auth'
import {
  createAuthKey,
  deleteAuthKey,
  expireAuthKey,
  listAuthKeys,
  ApiError,
  isAbortError,
  type HeadscaleUser,
  type PreAuthKey,
} from '../api'
import { usePageTitle } from '../hooks/usePageTitle'

const TAG_ONLY = '__tag_only__'

// isExpired uses the server's verdict on the expiration rather than
// comparing it with this browser's clock, which may be off.
function isExpired(k: PreAuthKey): boolean {
  return (k.used && !k.reusable) || k.expired
}

function isActive(k: PreAuthKey): boolean {
  return !isExpired(k)
}

function aclTags(k: PreAuthKey): string[] {
  return k.aclTags ?? []
}

function matchesStatus(k: PreAuthKey, status: string): boolean {
  switch (status) {
    case 'active':
      return isActive(k)
    case 'expired':
      return isExpired(k)
    case 'reusable':
      return k.reusable
    case 'ephemeral':
      return k.ephemeral
    default:
      return true
  }
}

function matchesUser(k: PreAuthKey, userFilter: string): boolean {
  if (!userFilter) return true
  if (userFilter === TAG_ONLY) return k.user == null
  return k.user?.name === userFilter
}

export default function Settings() {
  usePageTitle('Settings')
  const [searchParams, setSearchParams] = useSearchParams()
  const userFilter = searchParams.get('user') ?? ''
  const status = searchParams.get('status') ?? ''
  const filtered = userFilter !== '' || status !== ''

  const [authKeys, setAuthKeys] = useState<PreAuthKey[]>([])
  const [users, setUsers] = useState<HeadscaleUser[]>([])
  const [canManage, setCanManage] = useState(false)
  const { user } = useAuth()
  const ownUserId = user?.HeadscaleUserID.Valid ? user.HeadscaleUserID.String : ''
  const [notConfigured, setNotConfigured] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const createRef = useRef<HTMLDialogElement>(null)

  const load = async (signal?: AbortSignal) => {
    try {
      const res = await listAuthKeys(signal)
      setAuthKeys(res.authKeys ?? [])
      setUsers(res.users ?? [])
      setCanManage(res.canManage)
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
  }, [])

  const updateFilter = (key: string, value: string) => {
    const next = new URLSearchParams(searchParams)
    if (value) next.set(key, value)
    else next.delete(key)
    setSearchParams(next)
  }

  const visible = authKeys.filter((k) => matchesUser(k, userFilter) && matchesStatus(k, status))

  return (
    <>
      <div className="flex items-center justify-between mb-4">
        <h1 className="text-2xl font-bold">Settings</h1>
      </div>

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
        <div className="card bg-base-100 shadow">
          <div className="card-body">
            <h2 className="card-title">Pre-Auth Keys</h2>
            <p className="text-sm opacity-60 -mt-2">
              Pre-authentication keys let a new machine join this tailnet without an interactive
              login.
            </p>

            <form className="flex flex-wrap gap-2 mt-2 mb-2" onSubmit={(e) => e.preventDefault()}>
              <select
                className="select select-bordered select-sm"
                value={userFilter}
                onChange={(e) => updateFilter('user', e.target.value)}
              >
                <option value="">All users</option>
                {canManage && <option value={TAG_ONLY}>Tag Only</option>}
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
                <option value="active">Active</option>
                <option value="expired">Used / Expired</option>
                <option value="reusable">Reusable</option>
                <option value="ephemeral">Ephemeral</option>
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
                className="btn btn-sm btn-primary ml-auto"
                onClick={() => createRef.current?.showModal()}
              >
                Create pre-auth key
              </button>
            </form>

            {visible.length > 0 ? (
              <div className="overflow-x-auto">
                <table className="table">
                  <thead>
                    <tr>
                      <th>Key</th>
                      <th>User</th>
                      <th>Tags</th>
                      <th>Reusable</th>
                      <th>Ephemeral</th>
                      <th>Expiration</th>
                      <th className="text-right">Actions</th>
                    </tr>
                  </thead>
                  <tbody>
                    {visible.map((k) => (
                      <AuthKeyRow key={k.id} authKey={k} onChange={load} />
                    ))}
                  </tbody>
                </table>
              </div>
            ) : (
              <div className="p-8 text-center opacity-60">
                {filtered ? 'No pre-auth keys match the current filters.' : 'No pre-auth keys yet.'}
              </div>
            )}
          </div>
        </div>
      )}

      <CreateAuthKeyDialog
        dialogRef={createRef}
        users={canManage ? users : users.filter((u) => u.id === ownUserId)}
        canManage={canManage}
        onCreated={load}
      />
    </>
  )
}

function AuthKeyRow({ authKey: k, onChange }: { authKey: PreAuthKey; onChange: () => void }) {
  const [busy, setBusy] = useState(false)
  const expired = isExpired(k)

  const handleExpire = async () => {
    if (!confirm('Expire this pre-auth key? It will no longer be usable to register new machines.'))
      return
    setBusy(true)
    try {
      await expireAuthKey(k.id)
      onChange()
    } finally {
      setBusy(false)
    }
  }

  const handleDelete = async () => {
    if (!confirm("Delete this pre-auth key? This can't be undone.")) return
    setBusy(true)
    try {
      await deleteAuthKey(k.id)
      onChange()
    } finally {
      setBusy(false)
    }
  }

  return (
    <tr>
      <td className="font-mono text-sm whitespace-nowrap">{k.key}</td>
      <td className="whitespace-nowrap">
        {k.user?.name ?? <span className="opacity-60">(Tag Only)</span>}
      </td>
      <td>
        <div className="flex flex-wrap gap-1">
          {aclTags(k).map((t) => (
            <span key={t} className="badge badge-outline badge-sm font-mono">
              {t}
            </span>
          ))}
        </div>
      </td>
      <td>{k.reusable ? 'Yes' : 'No'}</td>
      <td>{k.ephemeral ? 'Yes' : 'No'}</td>
      <td className="whitespace-nowrap">
        {expired ? (
          <span className="badge badge-error badge-sm">Expired</span>
        ) : k.expiration ? (
          new Date(k.expiration).toLocaleString()
        ) : (
          '—'
        )}
      </td>
      <td className="text-right whitespace-nowrap">
        <button
          type="button"
          className="btn btn-sm btn-ghost text-error"
          disabled={expired || busy}
          onClick={() => void handleExpire()}
        >
          Expire Key
        </button>
        <button
          type="button"
          className="btn btn-sm btn-ghost text-error"
          disabled={busy}
          onClick={() => void handleDelete()}
        >
          Delete
        </button>
      </td>
    </tr>
  )
}

// CreateAuthKeyDialog creates a pre-auth key. Without canManage, keys can
// only be untagged ones for the caller.
function CreateAuthKeyDialog({
  dialogRef,
  users,
  canManage,
  onCreated,
}: {
  dialogRef: React.RefObject<HTMLDialogElement | null>
  users: HeadscaleUser[]
  canManage: boolean
  onCreated: () => void
}) {
  const [tagOnly, setTagOnly] = useState(false)
  const [userId, setUserId] = useState('')
  const [tagsInput, setTagsInput] = useState('')
  const [expiryDays, setExpiryDays] = useState(90)
  const [reusable, setReusable] = useState(false)
  const [ephemeral, setEphemeral] = useState(false)
  const { organization } = useAuth()
  const loginServer = `${window.location.protocol}//${organization?.Domain}`
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [createdKey, setCreatedKey] = useState<string | null>(null)

  const reset = () => {
    setTagOnly(false)
    setUserId('')
    setTagsInput('')
    setExpiryDays(90)
    setReusable(false)
    setEphemeral(false)
    setError(null)
    setCreatedKey(null)
  }

  const close = () => {
    dialogRef.current?.close()
    reset()
  }

  const parsedTags = tagsInput
    .split(',')
    .map((t) => t.trim())
    .filter(Boolean)
    .map((t) => (t.startsWith('tag:') ? t : `tag:${t}`))

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    setError(null)
    if (!tagOnly && !userId) {
      setError('Choose a user, or toggle "Tag only" and provide ACL tags.')
      return
    }
    if (tagOnly && parsedTags.length === 0) {
      setError('At least one ACL tag is required for a tag-only key.')
      return
    }
    setBusy(true)
    try {
      const res = await createAuthKey({
        userId: tagOnly ? undefined : userId,
        aclTags: parsedTags,
        expiryDays,
        reusable,
        ephemeral,
      })
      setCreatedKey(res.authKey.key)
      onCreated()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <dialog ref={dialogRef} className="modal">
      <div className="modal-box text-left">
        {createdKey ? (
          <>
            <h3 className="text-lg font-bold mb-4">Pre-auth key created</h3>
            <p className="text-sm opacity-60 mb-2">
              Copy this key now &mdash; it won&rsquo;t be shown again.
            </p>
            <div className="mockup-code text-sm mb-4">
              <pre data-prefix="$">
                <code>{createdKey}</code>
              </pre>
            </div>
            <p className="text-sm opacity-60 mb-2">Use it to register a new machine:</p>
            <div className="mockup-code text-xs mb-4 overflow-x-auto">
              <pre data-prefix="$">
                <code>
                  tailscale up --login-server={loginServer} --authkey {createdKey}
                </code>
              </pre>
            </div>
            <div className="modal-action">
              <button
                type="button"
                className="btn"
                onClick={() => void navigator.clipboard.writeText(createdKey)}
              >
                Copy key
              </button>
              <button type="button" className="btn btn-primary" onClick={close}>
                Done
              </button>
            </div>
          </>
        ) : (
          <>
            <h3 className="text-lg font-bold mb-4">Create pre-auth key</h3>
            {error && (
              <div className="alert alert-error mb-4">
                <span>{error}</span>
              </div>
            )}
            <form className="space-y-4" onSubmit={(e) => void handleSubmit(e)}>
              {canManage && (
                <label className="fieldset-label cursor-pointer gap-3">
                  <input
                    type="checkbox"
                    className="checkbox checkbox-sm"
                    checked={tagOnly}
                    onChange={(e) => setTagOnly(e.target.checked)}
                  />
                  <span>Tag only (no owning user)</span>
                </label>
              )}

              {!tagOnly && (
                <fieldset className="fieldset">
                  <legend className="fieldset-legend">User</legend>
                  <select
                    className="select select-bordered w-full"
                    value={userId}
                    onChange={(e) => setUserId(e.target.value)}
                    required={!tagOnly}
                  >
                    <option value="">Select a user…</option>
                    {users.map((u) => (
                      <option key={u.id} value={u.id}>
                        {u.name}
                      </option>
                    ))}
                  </select>
                </fieldset>
              )}

              {canManage && (
                <fieldset className="fieldset">
                  <legend className="fieldset-legend">ACL tags{tagOnly && ' (required)'}</legend>
                  <input
                    type="text"
                    className="input input-bordered w-full font-mono"
                    placeholder="tag:server, tag:prod"
                    value={tagsInput}
                    onChange={(e) => setTagsInput(e.target.value)}
                  />
                  <p className="text-sm opacity-60 mt-1">
                    Comma-separated. The <code>tag:</code> prefix is added automatically.
                  </p>
                </fieldset>
              )}

              <fieldset className="fieldset">
                <legend className="fieldset-legend">Expires in (days)</legend>
                <input
                  type="number"
                  min={1}
                  className="input input-bordered w-full"
                  value={expiryDays}
                  onChange={(e) => setExpiryDays(Number(e.target.value))}
                  required
                />
              </fieldset>

              <label className="fieldset-label cursor-pointer gap-3">
                <input
                  type="checkbox"
                  className="checkbox checkbox-sm"
                  checked={reusable}
                  onChange={(e) => setReusable(e.target.checked)}
                />
                <span>Reusable</span>
              </label>
              <label className="fieldset-label cursor-pointer gap-3">
                <input
                  type="checkbox"
                  className="checkbox checkbox-sm"
                  checked={ephemeral}
                  onChange={(e) => setEphemeral(e.target.checked)}
                />
                <span>Ephemeral</span>
              </label>

              <div className="modal-action">
                <button type="submit" className="btn btn-primary" disabled={busy}>
                  Create
                </button>
                <button type="button" className="btn btn-ghost" onClick={close}>
                  Cancel
                </button>
              </div>
            </form>
          </>
        )}
      </div>
      <form method="dialog" className="modal-backdrop">
        <button>close</button>
      </form>
    </dialog>
  )
}
