import { useState } from 'react'
import { useMutation } from '@tanstack/react-query'
import Modal from './Modal'
import { ErrorAlert } from './ui'
import { errorMessage, renameMachine, setMachineRoutes, setMachineTags, type Machine } from '../api'

// useDialogAction runs a dialog's action: busy while it runs, its error shown
// in the dialog, and on success onDone (reload the list) and onClose.
export function useDialogAction(onClose: () => void, onDone: () => void) {
  const mutation = useMutation({
    mutationFn: (action: () => Promise<unknown>) => action(),
    onSuccess: () => {
      onDone()
      onClose()
    },
  })
  return {
    busy: mutation.isPending,
    error: mutation.error ? errorMessage(mutation.error) : null,
    run: (action: () => Promise<unknown>) => mutation.mutate(action),
  }
}

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
