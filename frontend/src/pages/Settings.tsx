import { useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useAuth } from '../auth'
import {
  createAuthKey,
  deleteAuthKey,
  expireAuthKey,
  errorMessage,
  isNotConfigured,
  listAuthKeys,
  type HeadscaleUser,
  type PreAuthKey,
} from '../api'
import { usePageTitle } from '../hooks/usePageTitle'
import { useConfirm } from '../lib/confirm'
import Modal from '../components/Modal'
import CopyButton from '../components/CopyButton'
import { ErrorAlert, PageHeader } from '../components/ui'

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

  const queryClient = useQueryClient()
  const query = useQuery({ queryKey: ['authKeys'], queryFn: ({ signal }) => listAuthKeys(signal) })
  const authKeys = query.data?.authKeys ?? []
  const users = query.data?.users ?? []
  const assignableUsers = query.data?.assignableUsers ?? []
  const canManage = query.data?.canManage ?? false
  const notConfigured = isNotConfigured(query.error)
  const [actionError, setActionError] = useState<string | null>(null)
  const error = actionError ?? (query.error && !notConfigured ? errorMessage(query.error) : null)
  const reload = () => {
    setActionError(null)
    void queryClient.invalidateQueries({ queryKey: ['authKeys'] })
  }
  const [creating, setCreating] = useState(false)

  const updateFilter = (key: string, value: string) => {
    const next = new URLSearchParams(searchParams)
    if (value) next.set(key, value)
    else next.delete(key)
    setSearchParams(next, { replace: true })
  }

  const visible = authKeys.filter((k) => matchesUser(k, userFilter) && matchesStatus(k, status))

  return (
    <>
      <PageHeader title="Settings" />

      <ErrorAlert error={error} />

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
                  onClick={() => setSearchParams({}, { replace: true })}
                >
                  Clear
                </button>
              )}
              <button
                type="button"
                className="btn btn-sm btn-primary ml-auto"
                onClick={() => setCreating(true)}
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
                      <AuthKeyRow
                        key={k.id}
                        authKey={k}
                        onChange={reload}
                        onError={setActionError}
                      />
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

      {creating && (
        <CreateAuthKeyDialog
          onClose={() => setCreating(false)}
          users={assignableUsers}
          canManage={canManage}
          onCreated={reload}
        />
      )}
    </>
  )
}

function AuthKeyRow({
  authKey: k,
  onChange,
  onError: reportError,
}: {
  authKey: PreAuthKey
  onChange: () => void
  onError: (message: string) => void
}) {
  const confirm = useConfirm()
  const onError = (err: unknown) => reportError(errorMessage(err))
  const expire = useMutation({
    mutationFn: () => expireAuthKey(k.id),
    onSuccess: onChange,
    onError,
  })
  const remove = useMutation({
    mutationFn: () => deleteAuthKey(k.id),
    onSuccess: onChange,
    onError,
  })
  const busy = expire.isPending || remove.isPending
  const expired = isExpired(k)

  const handleExpire = async () => {
    const ok = await confirm({
      title: 'Expire pre-auth key',
      message: 'It will no longer be usable to register new machines.',
      confirmLabel: 'Expire key',
      danger: true,
    })
    if (ok) expire.mutate()
  }

  const handleDelete = async () => {
    const ok = await confirm({
      title: 'Delete pre-auth key',
      message: "This can't be undone.",
      confirmLabel: 'Delete key',
      danger: true,
    })
    if (ok) remove.mutate()
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
  onClose,
  users,
  canManage,
  onCreated,
}: {
  onClose: () => void
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
  const [validationError, setValidationError] = useState<string | null>(null)
  const [createdKey, setCreatedKey] = useState<string | null>(null)
  const create = useMutation({
    mutationFn: createAuthKey,
    onSuccess: (res) => {
      setCreatedKey(res.authKey.key)
      onCreated()
    },
  })
  const busy = create.isPending
  const error = validationError ?? (create.error ? errorMessage(create.error) : null)

  const parsedTags = tagsInput
    .split(',')
    .map((t) => t.trim())
    .filter(Boolean)
    .map((t) => (t.startsWith('tag:') ? t : `tag:${t}`))

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault()
    setValidationError(null)
    if (!tagOnly && !userId) {
      setValidationError('Choose a user, or toggle "Tag only" and provide ACL tags.')
      return
    }
    if (tagOnly && parsedTags.length === 0) {
      setValidationError('At least one ACL tag is required for a tag-only key.')
      return
    }
    create.mutate({
      userId: tagOnly ? undefined : userId,
      aclTags: parsedTags,
      expiryDays,
      reusable,
      ephemeral,
    })
  }

  return (
    <Modal
      open
      onClose={onClose}
      title={createdKey ? 'Pre-auth key created' : 'Create pre-auth key'}
    >
      {createdKey ? (
        <>
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
            <CopyButton text={createdKey}>Copy key</CopyButton>
            <button type="button" className="btn btn-primary" onClick={onClose}>
              Done
            </button>
          </div>
        </>
      ) : (
        <>
          <ErrorAlert error={error} />
          <form className="space-y-4" onSubmit={handleSubmit}>
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
              <button type="button" className="btn btn-ghost" onClick={onClose}>
                Cancel
              </button>
            </div>
          </form>
        </>
      )}
    </Modal>
  )
}
