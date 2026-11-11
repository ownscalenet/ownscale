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
  type Invite,
  type Role,
} from '../api'
import Modal from '../components/Modal'
import CopyButton, { CopyIcon } from '../components/CopyButton'
import { downloadCsv, toCsv } from '../lib/csv'
import { ErrorAlert, PageHeader, PageSpinner } from '../components/ui'

function roleLabel(role: string): string {
  return role
    .split('_')
    .map((w) => w[0].toUpperCase() + w.slice(1))
    .join(' ')
}

// parseNames splits newline-separated input, trims each line, and drops
// duplicates (case-insensitively, keeping the first casing seen).
function parseNames(input: string): string[] {
  const seen = new Set<string>()
  const names: string[] = []
  for (const raw of input.split('\n')) {
    const name = raw.trim()
    if (!name) continue
    const key = name.toLowerCase()
    if (seen.has(key)) continue
    seen.add(key)
    names.push(name)
  }
  return names
}

function invitesToCsv(invites: Invite[]): string {
  const header = ['Name', 'Role', 'Expires', 'Invite link']
  const rows = invites.map((i) => [
    i.name ?? '',
    roleLabel(i.role),
    i.expiresAt,
    `${window.location.origin}/invite/${i.token}`,
  ])
  return toCsv(header, rows)
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
        <div className="flex items-center gap-2">
          <button
            type="button"
            className="btn btn-primary btn-sm"
            onClick={() => setInviting(true)}
          >
            <UserPlus className="size-3.5" aria-hidden="true" />
            Invite member
          </button>
          <button
            type="button"
            className="btn btn-sm btn-outline"
            disabled={invites.length === 0}
            onClick={() => downloadCsv('invitations.csv', invitesToCsv(invites))}
          >
            Download CSV
          </button>
        </div>
      </PageHeader>

      <ErrorAlert error={error} />

      <div className="card bg-base-100 shadow mb-8">
        <div className="card-body p-0">
          <div className="overflow-x-auto">
            <table className="table">
              <thead>
                <tr>
                  <th>Name</th>
                  <th>Role</th>
                  <th>Expires</th>
                  <th className="text-right">Actions</th>
                </tr>
              </thead>
              <tbody>
                {invites.map((i) => (
                  <tr key={i.id}>
                    <td>
                      {i.name ? (
                        <span className="inline-flex items-center gap-1">
                          {i.name}
                          <CopyIcon text={i.name} label="name" />
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

// InviteResult is the outcome of creating one invite from a parsed name
// (or the single link-only invite, when no name was given).
interface InviteResult {
  name: string
  invite?: Invite
  error?: string
}

// InviteDialog creates one or more invite links and shows them for sharing.
function InviteDialog({ onClose, onCreated }: { onClose: () => void; onCreated: () => void }) {
  const [namesInput, setNamesInput] = useState('')
  const [role, setRole] = useState<Role>('member')
  const create = useMutation({
    mutationFn: async (): Promise<InviteResult[]> => {
      const names = parseNames(namesInput)
      // 0 or 1 name behaves exactly as before: a single createInvite call,
      // whose errors surface through create.error rather than a per-row
      // result.
      if (names.length <= 1) {
        const target = names[0] ?? ''
        const { invite } = await createInvite(target, role)
        return [{ name: target, invite }]
      }
      const settled = await Promise.allSettled(names.map((name) => createInvite(name, role)))
      return settled.map((r, i) => ({
        name: names[i],
        invite: r.status === 'fulfilled' ? r.value.invite : undefined,
        error: r.status === 'rejected' ? errorMessage(r.reason) : undefined,
      }))
    },
    onSuccess: onCreated,
  })
  const results = create.data

  return (
    <Modal open onClose={onClose} title="Invite new members">
      {results && results.length === 1 && results[0].invite ? (
        <div className="space-y-4">
          <p className="text-sm opacity-70">
            Share this link with the person you&rsquo;re inviting. It expires in 7 days.
          </p>
          <div className="join w-full">
            <input
              type="text"
              readOnly
              className="input input-bordered join-item w-full"
              value={`${window.location.origin}/invite/${results[0].invite.token}`}
            />
            <CopyButton
              text={`${window.location.origin}/invite/${results[0].invite.token}`}
              className="btn join-item"
            >
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
      ) : results ? (
        <div className="space-y-4">
          <p className="text-sm opacity-70">
            Share each link with the person it&rsquo;s for. They expire in 7 days.
          </p>
          <ul className="space-y-2">
            {results.map((r, i) => (
              <li key={i} className="flex items-center gap-2">
                <span className="text-sm flex-1 truncate">{r.name}</span>
                {r.invite ? (
                  <CopyButton
                    text={`${window.location.origin}/invite/${r.invite.token}`}
                    className="btn btn-xs btn-outline"
                  >
                    <Copy className="size-3.5" aria-hidden="true" />
                    Copy link
                  </CopyButton>
                ) : (
                  <span className="text-error text-xs">{r.error}</span>
                )}
              </li>
            ))}
          </ul>
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
            <legend className="fieldset-legend">
              Names - invites will have a name associated for easier identification.
            </legend>
            <textarea
              className="textarea textarea-bordered w-full"
              rows={3}
              placeholder="Optional"
              value={namesInput}
              onChange={(e) => setNamesInput(e.target.value)}
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
              Create invite link(s)
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
