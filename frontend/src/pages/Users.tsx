import { useEffect, useRef, useState } from 'react'
import { useAuth } from '../auth'
import {
  ROLES,
  createHeadscaleUser,
  createInvite,
  deleteHeadscaleUser,
  deleteInvite,
  linkMember,
  listHeadscaleUsers,
  listInvites,
  listMembers,
  removeMember,
  renameHeadscaleUser,
  transferOwnership,
  unlinkMember,
  updateMemberRole,
  type HeadscaleUser,
  type Invite,
  type Member,
  type Role,
} from '../api'

function roleLabel(role: string): string {
  return role
    .split('_')
    .map((w) => w[0].toUpperCase() + w.slice(1))
    .join(' ')
}

export default function Users() {
  const { user } = useAuth()
  const [members, setMembers] = useState<Member[]>([])
  const [canManage, setCanManage] = useState(false)
  const [callerRole, setCallerRole] = useState<Role>('member')
  const [hsUsers, setHsUsers] = useState<HeadscaleUser[]>([])
  const [hsNotConfigured, setHsNotConfigured] = useState(false)
  const [invites, setInvites] = useState<Invite[]>([])
  const [error, setError] = useState<string | null>(null)

  const inviteRef = useRef<HTMLDialogElement>(null)
  const createHsUserRef = useRef<HTMLDialogElement>(null)
  const [inviteEmail, setInviteEmail] = useState('')
  const [inviteRole, setInviteRole] = useState<Role>('member')
  const [inviteLink, setInviteLink] = useState<string | null>(null)
  const [newHsUserName, setNewHsUserName] = useState('')
  const [busy, setBusy] = useState(false)

  const loadMembers = async () => {
    try {
      const res = await listMembers()
      setMembers(res.members)
      setCanManage(res.canManage)
      setCallerRole(res.callerRole)
      setError(null)
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    }
  }

  const loadHeadscaleUsers = async () => {
    try {
      const res = await listHeadscaleUsers()
      setHsUsers(res.users ?? [])
      setHsNotConfigured(!!res.notConfigured)
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    }
  }

  const loadInvites = async () => {
    try {
      const res = await listInvites()
      setInvites(res.invites ?? [])
    } catch {
      // Non-managers get a 403 here; just leave the list empty.
    }
  }

  useEffect(() => {
    void loadMembers()
    void loadHeadscaleUsers()
    void loadInvites()
  }, [])

  const run = async (fn: () => Promise<unknown>) => {
    setBusy(true)
    try {
      await fn()
      await Promise.all([loadMembers(), loadHeadscaleUsers(), loadInvites()])
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <>
      <div className="flex items-center justify-between mb-4">
        <h1 className="text-2xl font-bold">Users</h1>
        {canManage && (
          <button
            type="button"
            className="btn btn-primary btn-sm"
            onClick={() => {
              setInviteEmail('')
              setInviteRole('member')
              setInviteLink(null)
              inviteRef.current?.showModal()
            }}
          >
            Invite member
          </button>
        )}
      </div>

      {error && (
        <div className="alert alert-error mb-4">
          <span>{error}</span>
        </div>
      )}

      <h2 className="text-lg font-semibold mb-2">Members</h2>
      <div className="card bg-base-100 shadow mb-8">
        <div className="card-body p-0">
          <div className="overflow-x-auto">
            <table className="table">
              <thead>
                <tr>
                  <th>Name</th>
                  <th>Email</th>
                  <th>Role</th>
                  <th>Linked headscale user</th>
                  {canManage && <th className="text-right">Actions</th>}
                </tr>
              </thead>
              <tbody>
                {members.map((m) => (
                  <MemberRow
                    key={m.id}
                    member={m}
                    caller={user?.ID === m.id}
                    canManage={canManage}
                    isOwner={callerRole === 'owner'}
                    hsUsers={hsUsers}
                    busy={busy}
                    run={run}
                  />
                ))}
              </tbody>
            </table>
          </div>
        </div>
      </div>

      {canManage && invites.length > 0 && (
        <>
          <h2 className="text-lg font-semibold mb-2">Pending invites</h2>
          <div className="card bg-base-100 shadow mb-8">
            <div className="card-body p-0">
              <div className="overflow-x-auto">
                <table className="table">
                  <thead>
                    <tr>
                      <th>Email</th>
                      <th>Role</th>
                      <th>Expires</th>
                      <th className="text-right">Actions</th>
                    </tr>
                  </thead>
                  <tbody>
                    {invites.map((i) => (
                      <tr key={i.id}>
                        <td>{i.email ?? <span className="opacity-60">(link only)</span>}</td>
                        <td>{roleLabel(i.role)}</td>
                        <td>{new Date(i.expiresAt).toLocaleString()}</td>
                        <td className="text-right">
                          <button
                            type="button"
                            className="btn btn-xs btn-outline mr-2"
                            onClick={() => {
                              const url = `${window.location.origin}/invite/${i.token}`
                              void navigator.clipboard.writeText(url)
                            }}
                          >
                            Copy link
                          </button>
                          <button
                            type="button"
                            className="btn btn-xs btn-outline btn-error"
                            disabled={busy}
                            onClick={() => void run(() => deleteInvite(i.id))}
                          >
                            Revoke
                          </button>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </div>
          </div>
        </>
      )}

      <div className="flex items-center justify-between mb-2">
        <h2 className="text-lg font-semibold">Headscale users</h2>
        {canManage && !hsNotConfigured && (
          <button
            type="button"
            className="btn btn-sm btn-outline"
            onClick={() => {
              setNewHsUserName('')
              createHsUserRef.current?.showModal()
            }}
          >
            Create user
          </button>
        )}
      </div>

      {hsNotConfigured ? (
        <div className="alert alert-warning">
          <span>
            Headscale isn&rsquo;t reachable for this organization yet. Try again in a moment.
          </span>
        </div>
      ) : (
        <div className="card bg-base-100 shadow">
          <div className="card-body p-0">
            <div className="overflow-x-auto">
              <table className="table">
                <thead>
                  <tr>
                    <th>Name</th>
                    <th>Created</th>
                    <th>Linked member</th>
                    {canManage && <th className="text-right">Actions</th>}
                  </tr>
                </thead>
                <tbody>
                  {hsUsers.map((u) => {
                    const linkedTo = members.find((m) => m.headscaleUserId === u.id)
                    return (
                      <HeadscaleUserRow
                        key={u.id}
                        hsUser={u}
                        linkedTo={linkedTo}
                        canManage={canManage}
                        busy={busy}
                        run={run}
                      />
                    )
                  })}
                </tbody>
              </table>
            </div>
          </div>
        </div>
      )}

      <dialog ref={inviteRef} className="modal">
        <div className="modal-box text-left">
          <h3 className="text-lg font-bold mb-4">Invite a member</h3>
          {inviteLink ? (
            <div className="space-y-4">
              <p className="text-sm opacity-70">
                Share this link with the person you&rsquo;re inviting. It expires in 7 days.
              </p>
              <div className="join w-full">
                <input type="text" readOnly className="input input-bordered join-item w-full" value={inviteLink} />
                <button
                  type="button"
                  className="btn join-item"
                  onClick={() => void navigator.clipboard.writeText(inviteLink)}
                >
                  Copy
                </button>
              </div>
              <div className="modal-action">
                <button type="button" className="btn" onClick={() => inviteRef.current?.close()}>
                  Done
                </button>
              </div>
            </div>
          ) : (
            <form
              className="space-y-4"
              onSubmit={(e) => {
                e.preventDefault()
                void run(async () => {
                  const res = await createInvite(inviteEmail, inviteRole)
                  setInviteLink(`${window.location.origin}/invite/${res.invite.token}`)
                })
              }}
            >
              <div className="form-control">
                <label className="label">
                  <span className="label-text">Email (optional, for your reference only)</span>
                </label>
                <input
                  type="email"
                  className="input input-bordered w-full"
                  value={inviteEmail}
                  onChange={(e) => setInviteEmail(e.target.value)}
                />
              </div>
              <div className="form-control">
                <label className="label">
                  <span className="label-text">Role</span>
                </label>
                <select
                  className="select select-bordered w-full"
                  value={inviteRole}
                  onChange={(e) => setInviteRole(e.target.value as Role)}
                >
                  {ROLES.filter((r) => r !== 'owner').map((r) => (
                    <option key={r} value={r}>
                      {roleLabel(r)}
                    </option>
                  ))}
                </select>
              </div>
              <div className="modal-action">
                <button type="submit" className="btn btn-primary" disabled={busy}>
                  Create invite link
                </button>
                <button type="button" className="btn btn-ghost" onClick={() => inviteRef.current?.close()}>
                  Cancel
                </button>
              </div>
            </form>
          )}
        </div>
        <form method="dialog" className="modal-backdrop">
          <button>close</button>
        </form>
      </dialog>

      <dialog ref={createHsUserRef} className="modal">
        <div className="modal-box text-left">
          <h3 className="text-lg font-bold mb-4">Create a headscale user</h3>
          <form
            className="space-y-4"
            onSubmit={(e) => {
              e.preventDefault()
              void run(() => createHeadscaleUser(newHsUserName)).then(() => createHsUserRef.current?.close())
            }}
          >
            <input
              type="text"
              className="input input-bordered w-full"
              placeholder="Name"
              value={newHsUserName}
              onChange={(e) => setNewHsUserName(e.target.value)}
              required
            />
            <div className="modal-action">
              <button type="submit" className="btn btn-primary" disabled={busy}>
                Create
              </button>
              <button type="button" className="btn btn-ghost" onClick={() => createHsUserRef.current?.close()}>
                Cancel
              </button>
            </div>
          </form>
        </div>
        <form method="dialog" className="modal-backdrop">
          <button>close</button>
        </form>
      </dialog>
    </>
  )
}

function MemberRow({
  member: m,
  caller,
  canManage,
  isOwner,
  hsUsers,
  busy,
  run,
}: {
  member: Member
  caller: boolean
  canManage: boolean
  isOwner: boolean
  hsUsers: HeadscaleUser[]
  busy: boolean
  run: (fn: () => Promise<unknown>) => Promise<void>
}) {
  const linkRef = useRef<HTMLDialogElement>(null)
  const [selectedHsUser, setSelectedHsUser] = useState(m.headscaleUserId ?? '')
  const canLink = canManage || caller
  const canRemove = canManage && m.role !== 'owner' && !caller

  return (
    <tr>
      <td className="font-medium">
        {m.name}
        {caller && <span className="badge badge-ghost badge-sm ml-2">You</span>}
      </td>
      <td>{m.email}</td>
      <td>
        {canManage && m.role !== 'owner' ? (
          <select
            className="select select-bordered select-sm"
            value={m.role}
            disabled={busy}
            onChange={(e) => void run(() => updateMemberRole(m.id, e.target.value as Role))}
          >
            {ROLES.filter((r) => r !== 'owner').map((r) => (
              <option key={r} value={r}>
                {roleLabel(r)}
              </option>
            ))}
          </select>
        ) : (
          <span className="badge badge-outline">{roleLabel(m.role)}</span>
        )}
      </td>
      <td>
        {m.headscaleUserName ?? (m.headscaleUserId ? <span className="font-mono">{m.headscaleUserId}</span> : <span className="opacity-60">Unlinked</span>)}
      </td>
      {canManage && (
        <td className="text-right whitespace-nowrap">
          {canLink && (
            <button
              type="button"
              className="btn btn-xs btn-outline mr-2"
              disabled={busy}
              onClick={() => {
                setSelectedHsUser(m.headscaleUserId ?? '')
                linkRef.current?.showModal()
              }}
            >
              {m.headscaleUserId ? 'Change link' : 'Link'}
            </button>
          )}
          {isOwner && !caller && (
            <button
              type="button"
              className="btn btn-xs btn-outline mr-2"
              disabled={busy}
              onClick={() => {
                if (confirm(`Transfer ownership to ${m.name}? You will become an admin.`)) {
                  void run(() => transferOwnership(m.id))
                }
              }}
            >
              Make owner
            </button>
          )}
          {canRemove && (
            <button
              type="button"
              className="btn btn-xs btn-outline btn-error"
              disabled={busy}
              onClick={() => {
                if (confirm(`Remove ${m.name} from this organization?`)) {
                  void run(() => removeMember(m.id))
                }
              }}
            >
              Remove
            </button>
          )}

          <dialog ref={linkRef} className="modal">
            <div className="modal-box text-left">
              <h3 className="text-lg font-bold mb-4">Link {m.name} to a headscale user</h3>
              <div className="space-y-4">
                <select
                  className="select select-bordered w-full"
                  value={selectedHsUser}
                  onChange={(e) => setSelectedHsUser(e.target.value)}
                >
                  <option value="">Select a headscale user…</option>
                  {hsUsers.map((u) => (
                    <option key={u.id} value={u.id}>
                      {u.name}
                    </option>
                  ))}
                </select>
                <div className="modal-action">
                  {m.headscaleUserId && (
                    <button
                      type="button"
                      className="btn btn-outline btn-error"
                      disabled={busy}
                      onClick={() => void run(() => unlinkMember(m.id)).then(() => linkRef.current?.close())}
                    >
                      Unlink
                    </button>
                  )}
                  <button
                    type="button"
                    className="btn btn-primary"
                    disabled={busy || !selectedHsUser}
                    onClick={() =>
                      void run(() => linkMember(m.id, selectedHsUser)).then(() => linkRef.current?.close())
                    }
                  >
                    Save
                  </button>
                  <button type="button" className="btn btn-ghost" onClick={() => linkRef.current?.close()}>
                    Cancel
                  </button>
                </div>
              </div>
            </div>
            <form method="dialog" className="modal-backdrop">
              <button>close</button>
            </form>
          </dialog>
        </td>
      )}
    </tr>
  )
}

function HeadscaleUserRow({
  hsUser: u,
  linkedTo,
  canManage,
  busy,
  run,
}: {
  hsUser: HeadscaleUser
  linkedTo: Member | undefined
  canManage: boolean
  busy: boolean
  run: (fn: () => Promise<unknown>) => Promise<void>
}) {
  const renameRef = useRef<HTMLDialogElement>(null)
  const [name, setName] = useState(u.name)

  return (
    <tr>
      <td>{u.name}</td>
      <td>{u.createdAt ? new Date(u.createdAt).toLocaleString() : '—'}</td>
      <td>{linkedTo ? linkedTo.name : <span className="opacity-60">Unlinked</span>}</td>
      {canManage && (
        <td className="text-right whitespace-nowrap">
          <button
            type="button"
            className="btn btn-xs btn-outline mr-2"
            onClick={() => {
              setName(u.name)
              renameRef.current?.showModal()
            }}
          >
            Rename
          </button>
          <button
            type="button"
            className="btn btn-xs btn-outline btn-error"
            disabled={busy}
            onClick={() => {
              if (confirm(`Delete headscale user ${u.name}? This can't be undone.`)) {
                void run(() => deleteHeadscaleUser(u.id))
              }
            }}
          >
            Delete
          </button>

          <dialog ref={renameRef} className="modal">
            <div className="modal-box text-left">
              <h3 className="text-lg font-bold mb-4">Rename {u.name}</h3>
              <form
                className="space-y-4"
                onSubmit={(e) => {
                  e.preventDefault()
                  void run(() => renameHeadscaleUser(u.id, name)).then(() => renameRef.current?.close())
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
                  <button type="button" className="btn btn-ghost" onClick={() => renameRef.current?.close()}>
                    Cancel
                  </button>
                </div>
              </form>
            </div>
            <form method="dialog" className="modal-backdrop">
              <button>close</button>
            </form>
          </dialog>
        </td>
      )}
    </tr>
  )
}
