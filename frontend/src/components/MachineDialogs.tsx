import { useState } from 'react'
import Modal from './Modal'
import { ErrorAlert } from './ui'
import { useDialogAction } from '../hooks/useDialogAction'
import {
  renameMachine,
  setMachineExpiry,
  setMachineRoutes,
  setMachineTags,
  type Machine,
} from '../api'

export function RenameDialog({
  machine: m,
  onClose,
  onDone,
}: {
  machine: Machine
  onClose: () => void
  onDone: () => void
}) {
  const [name, setName] = useState(m.givenName)
  const { busy, error, run } = useDialogAction(onClose, onDone)
  return (
    <Modal open onClose={onClose} title={`Rename ${m.givenName}`}>
      <ErrorAlert error={error} />
      <form
        className="space-y-4"
        onSubmit={(e) => {
          e.preventDefault()
          run(() => renameMachine(m.id, name))
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
          <button type="button" className="btn btn-ghost" onClick={onClose}>
            Cancel
          </button>
        </div>
      </form>
    </Modal>
  )
}

export function TagsDialog({
  machine: m,
  onClose,
  onDone,
}: {
  machine: Machine
  onClose: () => void
  onDone: () => void
}) {
  const [tagsInput, setTagsInput] = useState((m.tags ?? []).join(', '))
  const { busy, error, run } = useDialogAction(onClose, onDone)
  return (
    <Modal open onClose={onClose} title={`Edit ACL tags of ${m.givenName}`}>
      <ErrorAlert error={error} />
      <form
        className="space-y-4"
        onSubmit={(e) => {
          e.preventDefault()
          const parsed = tagsInput
            .split(',')
            .map((t) => t.trim())
            .filter(Boolean)
            .map((t) => (t.startsWith('tag:') ? t : `tag:${t}`))
          run(() => setMachineTags(m.id, parsed))
        }}
      >
        <input
          type="text"
          className="input input-bordered w-full font-mono"
          placeholder="tag:server, tag:prod"
          value={tagsInput}
          onChange={(e) => setTagsInput(e.target.value)}
        />
        <p className="text-sm opacity-60">
          Comma-separated. The <code>tag:</code> prefix is added automatically.
        </p>
        <div className="modal-action">
          <button type="submit" className="btn btn-primary" disabled={busy}>
            Save
          </button>
          <button type="button" className="btn btn-ghost" onClick={onClose}>
            Cancel
          </button>
        </div>
      </form>
    </Modal>
  )
}

export function RoutesDialog({
  machine: m,
  onClose,
  onDone,
}: {
  machine: Machine
  onClose: () => void
  onDone: () => void
}) {
  const [selectedRoutes, setSelectedRoutes] = useState<string[]>(m.approvedRoutes ?? [])
  const { busy, error, run } = useDialogAction(onClose, onDone)
  const availableRoutes = m.availableRoutes ?? []
  return (
    <Modal open onClose={onClose} title={`Edit route settings of ${m.givenName}`}>
      <ErrorAlert error={error} />
      <form
        className="space-y-4"
        onSubmit={(e) => {
          e.preventDefault()
          run(() => setMachineRoutes(m.id, selectedRoutes))
        }}
      >
        {availableRoutes.length > 0 ? (
          <ul className="space-y-2">
            {availableRoutes.map((route) => (
              <li key={route}>
                <label className="fieldset-label cursor-pointer gap-3">
                  <input
                    type="checkbox"
                    className="checkbox checkbox-sm"
                    checked={selectedRoutes.includes(route)}
                    onChange={(e) => {
                      setSelectedRoutes((prev) =>
                        e.target.checked ? [...prev, route] : prev.filter((r) => r !== route),
                      )
                    }}
                  />
                  <span className="font-mono text-sm">{route}</span>
                </label>
              </li>
            ))}
          </ul>
        ) : (
          <p className="text-sm opacity-60">This machine hasn&rsquo;t advertised any routes.</p>
        )}
        <div className="modal-action">
          <button
            type="submit"
            className="btn btn-primary"
            disabled={busy || availableRoutes.length === 0}
          >
            Save
          </button>
          <button type="button" className="btn btn-ghost" onClick={onClose}>
            Cancel
          </button>
        </div>
      </form>
    </Modal>
  )
}

const expiryPresetDays = [30, 90, 180]

// ExpiryDialog turns key expiry off for a machine that has one, or sets a new
// expiry date for a machine that never expires.
export function ExpiryDialog({
  machine: m,
  onClose,
  onDone,
}: {
  machine: Machine
  onClose: () => void
  onDone: () => void
}) {
  const disabling = !!m.expiry
  const [days, setDays] = useState('90')
  const [date, setDate] = useState('')
  const { busy, error, run } = useDialogAction(onClose, onDone)

  const custom = days === 'custom'
  const expiry = custom
    ? date
      ? new Date(`${date}T23:59:59`)
      : null
    : new Date(Date.now() + Number(days) * 24 * 60 * 60 * 1000)
  const valid = disabling || (expiry !== null && expiry.getTime() > Date.now())

  return (
    <Modal
      open
      onClose={onClose}
      title={`${disabling ? 'Disable' : 'Enable'} key expiry of ${m.givenName}`}
    >
      <ErrorAlert error={error} />
      <form
        className="space-y-4"
        onSubmit={(e) => {
          e.preventDefault()
          if (disabling) {
            run(() => setMachineExpiry(m.id, { mode: 'disable' }))
          } else if (expiry) {
            run(() => setMachineExpiry(m.id, { mode: 'enable', expiry: expiry.toISOString() }))
          }
        }}
      >
        {disabling ? (
          <p className="text-sm">
            The machine&rsquo;s key will never expire, so it stays connected until you expire or
            remove it.
          </p>
        ) : (
          <>
            <p className="text-sm">The machine&rsquo;s key will expire on the chosen date.</p>
            <select
              className="select select-bordered w-full"
              value={days}
              onChange={(e) => setDays(e.target.value)}
            >
              {expiryPresetDays.map((d) => (
                <option key={d} value={d}>
                  In {d} days
                </option>
              ))}
              <option value="custom">Custom date</option>
            </select>
            {custom && (
              <input
                type="date"
                className="input input-bordered w-full"
                aria-label="Expiry date"
                value={date}
                min={new Date(Date.now() + 24 * 60 * 60 * 1000).toISOString().slice(0, 10)}
                onChange={(e) => setDate(e.target.value)}
                required
              />
            )}
          </>
        )}
        <div className="modal-action">
          <button type="submit" className="btn btn-primary" disabled={busy || !valid}>
            {disabling ? 'Disable expiry' : 'Enable expiry'}
          </button>
          <button type="button" className="btn btn-ghost" onClick={onClose}>
            Cancel
          </button>
        </div>
      </form>
    </Modal>
  )
}
