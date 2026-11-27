import { useState } from 'react'
import { registerMachine, type AssignableUser } from '../api'
import { useAuth } from '../authContext'
import { useDialogAction } from '../hooks/useDialogAction'
import { rawTextProps } from '../lib/inputProps'
import Modal from './Modal'
import { ErrorAlert } from './ui'

// WaitingDeviceDialog registers a device that is waiting at its login URL
// to a chosen member, so an admin can set up a device for someone else
// without signing in on it.
export default function WaitingDeviceDialog({
  users,
  onClose,
  onDone,
}: {
  users: AssignableUser[]
  onClose: () => void
  onDone: () => void
}) {
  const [memberId, setMemberId] = useState('')
  const [key, setKey] = useState('')
  const [validation, setValidation] = useState<string | null>(null)
  const { refresh } = useAuth()
  // The first registration starts the org's headscale, changing its status.
  const { busy, error, run } = useDialogAction(onClose, () => {
    onDone()
    void refresh()
  })

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault()
    setValidation(null)
    if (!memberId) {
      setValidation('Choose a user to own this device.')
      return
    }
    if (!key.trim()) {
      setValidation('Paste the registration key or URL shown by `tailscale up`.')
      return
    }
    run(() => registerMachine(memberId, key.trim()))
  }

  return (
    <Modal open onClose={onClose} title="Register waiting device">
      <p className="text-sm opacity-60 mb-2">
        For a device that ran <code>tailscale up</code> and is showing a login URL.
      </p>
      <ErrorAlert error={validation ?? error} />
      <form className="space-y-4" onSubmit={handleSubmit}>
        <fieldset className="fieldset">
          <legend className="fieldset-legend">Login URL or registration key</legend>
          <input
            {...rawTextProps}
            id="register-machine-key"
            type="text"
            className="input input-bordered w-full font-mono"
            placeholder="https://…/register/… or hskey-authreq-…"
            value={key}
            onChange={(e) => setKey(e.target.value)}
            required
          />
        </fieldset>
        <fieldset className="fieldset">
          <legend className="fieldset-legend">Owner</legend>
          <select
            id="register-machine-user"
            className="select select-bordered w-full"
            value={memberId}
            onChange={(e) => setMemberId(e.target.value)}
            required
          >
            <option value="" disabled>
              Select a user
            </option>
            {users.map((u) => (
              <option key={u.id} value={u.id}>
                {u.name || u.email}
              </option>
            ))}
          </select>
        </fieldset>
        <div className="modal-action">
          <button type="submit" className="btn btn-primary" disabled={busy}>
            Register
          </button>
          <button type="button" className="btn btn-ghost" onClick={onClose}>
            Cancel
          </button>
        </div>
      </form>
    </Modal>
  )
}
