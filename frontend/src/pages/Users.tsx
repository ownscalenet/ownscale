import { RelativeTime } from '../components/RelativeTime'
import { Ban, Copy, Crown, Link, Trash2, UserPlus } from 'lucide-react'
import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useAuth } from '../authContext'
import { usePageTitle } from '../hooks/usePageTitle'
import {
  ROLES,
  createInvite,
  deleteInvite,
  errorMessage,
  listInvites,
  listMembers,
  isPasskeyEmail,
  removeMember,
  transferOwnership,
  updateMemberRole,
  type Member,
  type Role,
} from '../api'
import { useConfirm } from '../lib/confirm'
import Modal from '../components/Modal'
import CopyButton, { CopyIcon } from '../components/CopyButton'
import { ErrorAlert, PageHeader } from '../components/ui'

function roleLabel(role: string): string {
  return role
    .split('_')
    .map((w) => w[0].toUpperCase() + w.slice(1))
    .join(' ')
}

export default function Users() {
  usePageTitle('Users')
  const { user } = useAuth()
  const queryClient = useQueryClient()
  const membersQuery = useQuery({
    queryKey: ['members'],
    queryFn: ({ signal }) => listMembers(signal),
  })
  const members = membersQuery.data?.members ?? []
  const canManage = membersQuery.data?.canManage ?? false
  const callerRole = membersQuery.data?.callerRole ?? 'member'
  // Only managers may list invites.
  const invitesQuery = useQuery({
    queryKey: ['invites'],
    queryFn: ({ signal }) => listInvites(signal),
    enabled: canManage,
  })
  const invites = invitesQuery.data?.invites ?? []

  const [inviting, setInviting] = useState(false)

  // action runs a member or invite change, then reloads both lists.
  const action = useMutation({
    mutationFn: (fn: () => Promise<unknown>) => fn(),
    onSuccess: () =>
      Promise.all([
        queryClient.invalidateQueries({ queryKey: ['members'] }),
        queryClient.invalidateQueries({ queryKey: ['invites'] }),
      ]),
  })
  const run = (fn: () => Promise<unknown>) => action.mutate(fn)
  const busy = action.isPending
  const queryError = membersQuery.error ?? invitesQuery.error
  const error = action.error
    ? errorMessage(action.error)
    : queryError
      ? errorMessage(queryError)
      : null

  return (
    <>
      <PageHeader
        title="Users"
        subtitle={`${members.length} ${members.length === 1 ? 'member' : 'members'}${canManage ? `, ${invites.length} pending ${invites.length === 1 ? 'invite' : 'invites'}` : ''}`}
      >
        {canManage && (
          <button
            type="button"
            className="btn btn-primary btn-sm"
            onClick={() => setInviting(true)}
          >
            <UserPlus className="size-3.5" aria-hidden="true" />
            Invite member
          </button>
        )}
      </PageHeader>

      <ErrorAlert error={error} />

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
                        <td>
                          {i.email ? (
                            <span className="inline-flex items-center gap-1">
                              {i.email}
                              <CopyIcon text={i.email} label="email" />
                            </span>
                          ) : (
                            <span className="opacity-60">(link only)</span>
                          )}
                        </td>
                        <td>{roleLabel(i.role)}</td>
                        <td>
                          <RelativeTime value={i.expiresAt} />
                        </td>
                        <td className="text-right">
                          <CopyButton
                            text={`${window.location.origin}/invite/${i.token}`}
                            className="btn btn-xs btn-outline mr-2"
                          >
                            <Copy className="size-3.5" aria-hidden="true" />
                            Copy link
                          </CopyButton>
                          <button
                            type="button"
                            className="btn btn-xs btn-outline btn-error"
                            disabled={busy}
                            onClick={() => run(() => deleteInvite(i.id))}
                          >
                            <Ban className="size-3.5" aria-hidden="true" />
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

      {inviting && (
        <InviteDialog
          onClose={() => setInviting(false)}
          onCreated={() => queryClient.invalidateQueries({ queryKey: ['invites'] })}
        />
      )}
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
  run: (fn: () => Promise<unknown>) => void
}) {
  const confirm = useConfirm()
  const canRemove = canManage && m.role !== 'owner' && !caller

  return (
    <tr>
      <td className="font-medium">
        <span className="inline-flex items-center gap-1">
          {m.name}
          <CopyIcon text={m.name} label="name" />
        </span>
        {caller && <span className="badge badge-ghost badge-sm ml-2">You</span>}
      </td>
      <td>
        <span className="inline-flex items-center gap-1">
          {m.email}
          <CopyIcon text={m.email} label="email" />
        </span>
      </td>
      <td>
        {canManage && m.role !== 'owner' ? (
          <select
            className="select select-bordered select-sm"
            value={m.role}
            disabled={busy}
            onChange={(e) => {
              // Read now: the action runs later, after React resets the select.
              const role = e.target.value as Role
              run(() => updateMemberRole(m.id, role))
            }}
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
              onClick={async () => {
                const ok = await confirm({
                  title: `Transfer ownership to ${m.name}?`,
                  consequences: ['You will become an admin.'],
                  confirmLabel: 'Transfer ownership',
                })
                if (ok) run(() => transferOwnership(m.id))
              }}
            >
              <Crown className="size-3.5" aria-hidden="true" />
              Make owner
            </button>
          )}
          {canRemove && (
            <button
              type="button"
              className="btn btn-xs btn-outline btn-error"
              disabled={busy}
              onClick={async () => {
                const ok = await confirm({
                  title: `Remove ${m.name} from this organization?`,
                  consequences: [
                    'Their machines will be removed from the tailnet.',
                    'They lose access to this organization.',
                  ],
                  confirmLabel: 'Remove member',
                  tone: 'danger',
                  typeToConfirm: isPasskeyEmail(m.email) ? m.name : m.email,
                })
                if (ok) run(() => removeMember(m.id))
              }}
            >
              <Trash2 className="size-3.5" aria-hidden="true" />
              Remove
            </button>
          )}
        </td>
      )}
    </tr>
  )
}

// InviteDialog creates an invite link and shows it for sharing.
function InviteDialog({ onClose, onCreated }: { onClose: () => void; onCreated: () => void }) {
  const [email, setEmail] = useState('')
  const [role, setRole] = useState<Role>('member')
  const create = useMutation({
    mutationFn: () => createInvite(email, role),
    onSuccess: onCreated,
  })
  const link = create.data ? `${window.location.origin}/invite/${create.data.invite.token}` : null

  return (
    <Modal open onClose={onClose} title="Invite a member">
      {link ? (
        <div className="space-y-4">
          <p className="text-sm opacity-70">
            Share this link with the person you&rsquo;re inviting. It expires in 7 days.
          </p>
          <div className="join w-full">
            <input
              type="text"
              readOnly
              className="input input-bordered join-item w-full"
              value={link}
            />
            <CopyButton text={link} className="btn join-item">
              <Copy className="size-3.5" aria-hidden="true" />
              Copy
            </CopyButton>
          </div>
          <div className="modal-action">
            <button type="button" className="btn" onClick={onClose}>
              Done
            </button>
          </div>
        </div>
      ) : (
        <form
          className="space-y-4"
          onSubmit={(e) => {
            e.preventDefault()
            create.mutate()
          }}
        >
          <ErrorAlert error={create.error ? errorMessage(create.error) : null} />
          <fieldset className="fieldset">
            <legend className="fieldset-legend">Email (optional, for your reference only)</legend>
            <input
              type="email"
              className="input input-bordered w-full"
              value={email}
              onChange={(e) => setEmail(e.target.value)}
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
              value={role}
              onChange={(e) => setRole(e.target.value as Role)}
            >
              {ROLES.filter((r) => r !== 'owner').map((r) => (
                <option key={r} value={r}>
                  {roleLabel(r)}
                </option>
              ))}
            </select>
          </fieldset>
          <div className="modal-action">
            <button type="submit" className="btn btn-primary" disabled={create.isPending}>
              <Link className="size-3.5" aria-hidden="true" />
              Create invite link
            </button>
            <button type="button" className="btn btn-ghost" onClick={onClose}>
              Cancel
            </button>
          </div>
        </form>
      )}
    </Modal>
  )
}
