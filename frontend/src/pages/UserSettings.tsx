import { RelativeTime } from '../components/RelativeTime'
import { KeyRound, Trash2, UserX } from 'lucide-react'
import { Link } from 'react-router-dom'
import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  listPasskeys,
  listMembers,
  deleteAccount,
  deletePasskey,
  errorMessage,
  passkeyAddBegin,
  passkeyAddFinish,
  type Passkey,
} from '../api'
import { createPasskey, browserSupportsWebAuthn } from '../lib/webauthn'
import { usePageTitle } from '../hooks/usePageTitle'
import { useConfirm } from '../lib/confirm'
import { useAuth } from '../authContext'
import Modal from '../components/Modal'
import { ErrorAlert, PageHeader } from '../components/ui'

export default function UserSettings() {
  usePageTitle('User settings')

  return (
    <>
      <PageHeader title="User settings" />

      <PasskeysCard />
      <DeleteAccountCard />
    </>
  )
}

function PasskeysCard() {
  const confirm = useConfirm()
  const supported = browserSupportsWebAuthn()
  const queryClient = useQueryClient()
  const query = useQuery({ queryKey: ['passkeys'], queryFn: ({ signal }) => listPasskeys(signal) })
  const passkeys = query.data?.passkeys ?? []
  const reload = () => queryClient.invalidateQueries({ queryKey: ['passkeys'] })

  const add = useMutation({
    mutationFn: async (name: string) => {
      const options = await passkeyAddBegin(name)
      await passkeyAddFinish(await createPasskey(options))
    },
    onSuccess: reload,
  })
  const remove = useMutation({ mutationFn: (p: Passkey) => deletePasskey(p.id), onSuccess: reload })
  const busy = add.isPending || remove.isPending
  const failure = add.error ?? remove.error ?? query.error
  const error = failure ? errorMessage(failure) : null

  const [naming, setNaming] = useState(false)

  const handleAdd = (name: string) => {
    setNaming(false)
    remove.reset()
    add.mutate(name)
  }

  const handleDelete = async (p: Passkey) => {
    const ok = await confirm({
      title: `Delete the passkey "${p.name}"?`,
      consequences: ["You won't be able to sign in with it anymore.", "This can't be undone."],
      confirmLabel: 'Delete passkey',
      tone: 'danger',
    })
    if (!ok) return
    add.reset()
    remove.mutate(p)
  }

  return (
    <div className="card bg-base-100 shadow">
      <div className="card-body">
        {naming && <PasskeyNameDialog onClose={() => setNaming(false)} onSubmit={handleAdd} />}
        <div className="flex flex-wrap items-center justify-between gap-2">
          <div>
            <h2 className="card-title">Passkeys</h2>
            <p className="text-sm opacity-60 -mt-1">
              Passkeys let you sign in to the console without a password, using your device&rsquo;s
              biometrics or security key.
            </p>
          </div>
          {supported && (
            <button
              type="button"
              className="btn btn-sm btn-primary"
              disabled={busy}
              onClick={() => setNaming(true)}
            >
              <KeyRound className="size-3.5" aria-hidden="true" />
              Add a passkey
            </button>
          )}
        </div>

        <ErrorAlert error={error} />

        {!supported && (
          <div className="alert alert-warning mt-2">
            <span>This browser doesn&rsquo;t support passkeys.</span>
          </div>
        )}

        {passkeys.length > 0 ? (
          <div className="overflow-x-auto mt-2">
            <table className="table">
              <thead>
                <tr>
                  <th>Name</th>
                  <th>Created</th>
                  <th>Last used</th>
                  <th className="text-right">Actions</th>
                </tr>
              </thead>
              <tbody>
                {passkeys.map((p) => (
                  <tr key={p.id}>
                    <td>{p.name || 'Passkey'}</td>
                    <td className="whitespace-nowrap">
                      <RelativeTime value={p.createdAt} />
                    </td>
                    <td className="whitespace-nowrap">
                      {p.lastUsedAt ? <RelativeTime value={p.lastUsedAt} /> : '—'}
                    </td>
                    <td className="text-right whitespace-nowrap">
                      <button
                        type="button"
                        className="btn btn-sm btn-ghost text-error"
                        disabled={busy}
                        onClick={() => void handleDelete(p)}
                      >
                        <Trash2 className="size-3.5" aria-hidden="true" />
                        Delete
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        ) : (
          <div className="p-8 text-center opacity-60">No passkeys yet.</div>
        )}
      </div>
    </div>
  )
}

// DeleteAccountCard lets the user delete their own account. Owners must
// first hand over or empty the organization, which the server enforces too.
function DeleteAccountCard() {
  const confirm = useConfirm()
  const { refresh } = useAuth()
  const members = useQuery({ queryKey: ['members'], queryFn: ({ signal }) => listMembers(signal) })
  const remove = useMutation({ mutationFn: deleteAccount, onSuccess: () => void refresh() })
  const ownerWithMembers =
    members.data?.callerRole === 'owner' && (members.data?.members.length ?? 0) > 1
  const failure = remove.error ?? members.error
  const error = failure ? errorMessage(failure) : null

  const handleDelete = async () => {
    const ok = await confirm({
      title: 'Delete your account?',
      consequences: [
        'All your machines are removed from the tailnet.',
        'Your passkeys are deleted.',
        "This can't be undone.",
      ],
      confirmLabel: 'Delete account',
      tone: 'danger',
    })
    if (ok) remove.mutate()
  }

  return (
    <div className="card bg-base-100 shadow mt-6">
      <div className="card-body">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <div>
            <h2 className="card-title">Delete account</h2>
            <p className="text-sm opacity-60 -mt-1">
              Permanently delete your account and remove your machines from the tailnet.
            </p>
          </div>
          <button
            type="button"
            className="btn btn-sm btn-error"
            disabled={!members.data || ownerWithMembers || remove.isPending}
            onClick={() => void handleDelete()}
          >
            <UserX className="size-3.5" aria-hidden="true" />
            Delete account
          </button>
        </div>

        {ownerWithMembers && (
          <div className="alert alert-info mt-2">
            <span>
              You own this organization and it still has other members. Before deleting your
              account, either transfer ownership to another member or remove all other members on
              the{' '}
              <Link to="/users" className="link">
                Users
              </Link>{' '}
              page.
            </span>
          </div>
        )}

        <ErrorAlert error={error} />
      </div>
    </div>
  )
}

// PasskeyNameDialog asks for a name for a new passkey before creating it.
function PasskeyNameDialog({
  onClose,
  onSubmit,
}: {
  onClose: () => void
  onSubmit: (name: string) => void
}) {
  const [name, setName] = useState('Passkey')
  return (
    <Modal open onClose={onClose} title="Add a passkey">
      <form
        onSubmit={(e) => {
          e.preventDefault()
          onSubmit(name.trim())
        }}
      >
        <fieldset className="fieldset">
          <legend className="fieldset-legend">Name</legend>
          <input
            type="text"
            className="input input-bordered w-full"
            placeholder="MacBook Touch ID"
            value={name}
            onChange={(e) => setName(e.target.value)}
            autoFocus
          />
        </fieldset>
        <div className="modal-action">
          <button type="submit" className="btn btn-primary">
            <KeyRound className="size-3.5" aria-hidden="true" />
            Add passkey
          </button>
          <button type="button" className="btn btn-ghost" onClick={onClose}>
            Cancel
          </button>
        </div>
      </form>
    </Modal>
  )
}
