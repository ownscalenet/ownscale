import { useEffect, useState } from 'react'
import { getAuthRestrictions, updateAuthRestrictions, ApiError } from '../api'

// ListEditor renders a labeled card with an add-input and a removable
// chip/row list for one of the three OIDC allowlists (domains, groups,
// users). It keeps the list purely as local state; the parent owns
// persistence (a full-list PUT on every add/remove), mirroring headplane's
// per-action add/remove UX but backed by ownscale's replace-all API.
function ListEditor({
  title,
  description,
  placeholder,
  items,
  canManage,
  disabled,
  onAdd,
  onRemove,
}: {
  title: string
  description: string
  placeholder: string
  items: string[]
  canManage: boolean
  disabled: boolean
  onAdd: (value: string) => void
  onRemove: (value: string) => void
}) {
  const [value, setValue] = useState('')

  const submit = () => {
    const trimmed = value.trim()
    if (!trimmed) return
    onAdd(trimmed)
    setValue('')
  }

  return (
    <div className="card bg-base-100 shadow h-full">
      <div className="card-body">
        <h2 className="card-title">{title}</h2>
        <p className="text-sm text-base-content/70">{description}</p>

        {canManage && (
          <div className="join mt-2">
            <input
              type="text"
              className="input input-bordered join-item w-full"
              placeholder={placeholder}
              value={value}
              disabled={disabled}
              onChange={(e) => setValue(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter') {
                  e.preventDefault()
                  submit()
                }
              }}
            />
            <button type="button" className="btn btn-primary join-item" disabled={disabled} onClick={submit}>
              Add
            </button>
          </div>
        )}

        <ul className="mt-3 space-y-1">
          {items.length === 0 && <li className="text-sm text-base-content/60">No entries. Anyone can sign in.</li>}
          {items.map((item) => (
            <li key={item} className="flex items-center justify-between bg-base-200 rounded px-3 py-1.5 text-sm">
              <span className="font-mono">{item}</span>
              {canManage && (
                <button
                  type="button"
                  className="btn btn-ghost btn-xs"
                  disabled={disabled}
                  onClick={() => onRemove(item)}
                >
                  Remove
                </button>
              )}
            </li>
          ))}
        </ul>
      </div>
    </div>
  )
}

export default function AuthRestrictions() {
  const [domains, setDomains] = useState<string[]>([])
  const [groups, setGroups] = useState<string[]>([])
  const [users, setUsers] = useState<string[]>([])
  const [canManage, setCanManage] = useState(false)
  const [notConfigured, setNotConfigured] = useState(false)
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [success, setSuccess] = useState(false)

  useEffect(() => {
    void (async () => {
      try {
        const res = await getAuthRestrictions()
        setDomains(res.domains)
        setGroups(res.groups)
        setUsers(res.users)
        setCanManage(res.canManage)
        setNotConfigured(false)
      } catch (err) {
        if (err instanceof ApiError && err.status === 409) {
          setNotConfigured(true)
        } else {
          setError(err instanceof Error ? err.message : String(err))
        }
      } finally {
        setLoading(false)
      }
    })()
  }, [])

  const persist = async (nextDomains: string[], nextGroups: string[], nextUsers: string[]) => {
    setSaving(true)
    setError(null)
    setSuccess(false)
    try {
      const res = await updateAuthRestrictions(nextDomains, nextGroups, nextUsers)
      setDomains(res.domains)
      setGroups(res.groups)
      setUsers(res.users)
      setSuccess(true)
    } catch (err) {
      setError(err instanceof ApiError ? err.message : err instanceof Error ? err.message : String(err))
    } finally {
      setSaving(false)
    }
  }

  if (loading) {
    return (
      <div className="flex items-center justify-center h-64">
        <span className="loading loading-spinner loading-lg"></span>
      </div>
    )
  }

  return (
    <div>
      <h1 className="text-2xl font-bold mb-4">Authentication Restrictions</h1>

      {notConfigured && (
        <div className="alert alert-warning mb-4">
          <span>Your organization isn't active yet, so its authentication restrictions can't be edited.</span>
        </div>
      )}

      {error && (
        <div role="alert" className="alert alert-error mb-4">
          <span>{error}</span>
        </div>
      )}
      {success && (
        <div role="alert" className="alert alert-success mb-4">
          <span>Restrictions saved. Headscale is restarting to apply them.</span>
        </div>
      )}
      {!canManage && !notConfigured && (
        <p className="text-sm text-base-content/60 mb-4">Only owners and admins can edit authentication restrictions.</p>
      )}

      <div className="grid grid-cols-1 lg:grid-cols-3 gap-6">
        <ListEditor
          title="Allowed domains"
          description="Only users signing in with an email at one of these domains can join. Leave empty to allow any domain."
          placeholder="example.com"
          items={domains}
          canManage={canManage}
          disabled={saving || notConfigured}
          onAdd={(value) => void persist([...domains, value], groups, users)}
          onRemove={(value) => void persist(domains.filter((d) => d !== value), groups, users)}
        />
        <ListEditor
          title="Allowed groups"
          description="Only users whose IdP-provided groups include one of these can join. Leave empty to allow any group."
          placeholder="group:eng"
          items={groups}
          canManage={canManage}
          disabled={saving || notConfigured}
          onAdd={(value) => void persist(domains, [...groups, value], users)}
          onRemove={(value) => void persist(domains, groups.filter((g) => g !== value), users)}
        />
        <ListEditor
          title="Allowed users"
          description="Only these specific email addresses can join, in addition to any domain/group match above."
          placeholder="alice@example.com"
          items={users}
          canManage={canManage}
          disabled={saving || notConfigured}
          onAdd={(value) => void persist(domains, groups, [...users, value])}
          onRemove={(value) => void persist(domains, groups, users.filter((u) => u !== value))}
        />
      </div>
    </div>
  )
}
