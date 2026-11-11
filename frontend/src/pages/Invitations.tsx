import { RelativeTime } from '../components/RelativeTime'
import { Ban, Copy, Link, UserPlus } from 'lucide-react'
import { useState } from 'react'
import { Navigate } from 'react-router-dom'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { usePageTitle } from '../hooks/usePageTitle'
import {
  ROLES,
  createInvite,
  deleteInvite,
  errorMessage,
  listInvites,
  listMembers,
  type Role,
} from '../api'
import Modal from '../components/Modal'
import CopyButton, { CopyIcon } from '../components/CopyButton'
import { ErrorAlert, PageHeader, PageSpinner } from '../components/ui'

function roleLabel(role: string): string {
  return role
    .split('_')
    .map((w) => w[0].toUpperCase() + w.slice(1))
    .join(' ')
}

export default function Invitations() {
  usePageTitle('Invitations')
  const queryClient = useQueryClient()
  // Invites are manager-only; reuses the ['members'] cache shared with
  // Users/Layout so this doesn't trigger an extra request.
  const membersQuery = useQuery({
    queryKey: ['members'],
    queryFn: ({ signal }) => listMembers(signal),
  })
  const canManage = membersQuery.data?.canManage ?? false
  const invitesQuery = useQuery({
    queryKey: ['invites'],
    queryFn: ({ signal }) => listInvites(signal),
    enabled: canManage,
  })
  const invites = invitesQuery.data?.invites ?? []

  const [inviting, setInviting] = useState(false)

  const deleteAction = useMutation({
    mutationFn: (id: string) => deleteInvite(id),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['invites'] }),
  })
  const busy = deleteAction.isPending
  const error = deleteAction.error
    ? errorMessage(deleteAction.error)
    : invitesQuery.error
      ? errorMessage(invitesQuery.error)
      : null

  if (membersQuery.isLoading) {
    return <PageSpinner />
  }
  // Invite management is manager-only; a plain member navigating here
  // directly is bounced to Machines, same as the nav link being hidden.
  if (!canManage) {
    return <Navigate to="/machines" replace />
  }

  return (
    <>
      <PageHeader
        title="Invitations"
        subtitle={`${invites.length} pending ${invites.length === 1 ? 'invite' : 'invites'}`}
      >
        <button type="button" className="btn btn-primary btn-sm" onClick={() => setInviting(true)}>
          <UserPlus className="size-3.5" aria-hidden="true" />
          Invite member
        </button>
      </PageHeader>

      <ErrorAlert error={error} />

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
                        onClick={() => deleteAction.mutate(i.id)}
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

      {inviting && (
        <InviteDialog
          onClose={() => setInviting(false)}
          onCreated={() => queryClient.invalidateQueries({ queryKey: ['invites'] })}
        />
      )}
    </>
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
