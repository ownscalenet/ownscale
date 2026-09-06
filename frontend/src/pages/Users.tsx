import { useEffect, useRef, useState } from 'react'
import { useAuth } from '../auth'
import { usePageTitle } from '../hooks/usePageTitle'
import {
  ROLES,
  createInvite,
  deleteInvite,
  listInvites,
  listMembers,
  removeMember,
  transferOwnership,
  updateMemberRole,
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
  usePageTitle('Users')
  const { user } = useAuth()
  const [members, setMembers] = useState<Member[]>([])
  const [canManage, setCanManage] = useState(false)
  const [callerRole, setCallerRole] = useState<Role>('member')
  const [invites, setInvites] = useState<Invite[]>([])
  const [error, setError] = useState<string | null>(null)

  const inviteRef = useRef<HTMLDialogElement>(null)
  const [inviteEmail, setInviteEmail] = useState('')
  const [inviteRole, setInviteRole] = useState<Role>('member')
  const [inviteLink, setInviteLink] = useState<string | null>(null)
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
    void loadInvites()
  }, [])

  const run = async (fn: () => Promise<unknown>) => {
    setBusy(true)
    try {
      await fn()
      await Promise.all([loadMembers(), loadInvites()])
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

      <dialog ref={inviteRef} className="modal">
        <div className="modal-box text-left">
          <h3 className="text-lg font-bold mb-4">Invite a member</h3>
          {inviteLink ? (
            <div className="space-y-4">
              <p className="text-sm opacity-70">
                Share this link with the person you&rsquo;re inviting. It expires in 7 days.
              </p>
              <div className="join w-full">
                <input
                  type="text"
                  readOnly
                  className="input input-bordered join-item w-full"
                  value={inviteLink}
                />
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
              <fieldset className="fieldset">
                <legend className="fieldset-legend">
                  Email (optional, for your reference only)
                </legend>
                <input
                  type="email"
                  className="input input-bordered w-full"
                  value={inviteEmail}
                  onChange={(e) => setInviteEmail(e.target.value)}
                  autoComplete="off"
                  data-1p-ignore
                  data-lpignore="true"
                  data-bwignore
                  data-form-type="other"
                  data-protonpass-ignore
                />
              </fieldset>
              <fieldset className="fieldset">
                <legend className="fieldset-legend">Role</legend>
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
              </fieldset>
              <div className="modal-action">
                <button type="submit" className="btn btn-primary" disabled={busy}>
                  Create invite link
                </button>
                <button
                  type="button"
                  className="btn btn-ghost"
                  onClick={() => inviteRef.current?.close()}
                >
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
    </>
  )
}

function MemberRow({
  member: m,
  caller,
  canManage,
  isOwner,
  busy,
  run,
}: {
  member: Member
  caller: boolean
  canManage: boolean
  isOwner: boolean
  busy: boolean
  run: (fn: () => Promise<unknown>) => Promise<void>
}) {
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
      {canManage && (
        <td className="text-right whitespace-nowrap">
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
        </td>
      )}
    </tr>
  )
}
