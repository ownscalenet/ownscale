import { Crown, Trash2 } from 'lucide-react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useAuth } from '../authContext'
import { usePageTitle } from '../hooks/usePageTitle'
import {
  ROLES,
  errorMessage,
  listMembers,
  removeMember,
  transferOwnership,
  updateMemberRole,
  type Member,
  type Role,
} from '../api'
import { useConfirm } from '../lib/confirm'
import { downloadCsv, toCsv } from '../lib/csv'
import { CopyIcon } from '../components/CopyButton'
import { ErrorAlert, PageHeader } from '../components/ui'

function roleLabel(role: string): string {
  return role
    .split('_')
    .map((w) => w[0].toUpperCase() + w.slice(1))
    .join(' ')
}

function membersToCsv(members: Member[]): string {
  const header = ['Name', 'Email', 'Role', 'User ID']
  const rows = members.map((m) => [m.name, m.email, roleLabel(m.role), m.id])
  return toCsv(header, rows)
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

  const action = useMutation({
    mutationFn: (fn: () => Promise<unknown>) => fn(),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['members'] }),
  })
  const run = (fn: () => Promise<unknown>) => action.mutate(fn)
  const busy = action.isPending
  const error = action.error
    ? errorMessage(action.error)
    : membersQuery.error
      ? errorMessage(membersQuery.error)
      : null

  return (
    <>
      <PageHeader
        title="Users"
        subtitle={`${members.length} ${members.length === 1 ? 'member' : 'members'}`}
      >
        <button
          type="button"
          className="btn btn-sm btn-outline"
          disabled={members.length === 0}
          onClick={() => downloadCsv('users.csv', membersToCsv(members))}
        >
          Download CSV
        </button>
      </PageHeader>

      <ErrorAlert error={error} />

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
