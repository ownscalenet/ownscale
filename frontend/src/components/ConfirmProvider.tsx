import { useCallback, useRef, useState, type ReactNode } from 'react'
import { ConfirmContext, type Confirm, type ConfirmOptions } from '../lib/confirm'
import Modal from './Modal'

// ConfirmProvider renders the confirmation dialog that useConfirm asks for.
// Cancel has the initial focus, so Enter never confirms by accident.
export default function ConfirmProvider({ children }: { children: ReactNode }) {
  const [pending, setPending] = useState<ConfirmOptions | null>(null)
  const resolveRef = useRef<((ok: boolean) => void) | null>(null)

  const confirm = useCallback<Confirm>(
    (options) =>
      new Promise((resolve) => {
        // A newer request replaces one still open, which counts as cancelled.
        resolveRef.current?.(false)
        resolveRef.current = resolve
        setPending(options)
      }),
    [],
  )

  const finish = (ok: boolean) => {
    resolveRef.current?.(ok)
    resolveRef.current = null
    setPending(null)
  }

  return (
    <ConfirmContext.Provider value={confirm}>
      {children}
      <Modal open={pending !== null} onClose={() => finish(false)} title={pending?.title}>
        <p>{pending?.message}</p>
        <div className="modal-action">
          <button type="button" className="btn btn-ghost" autoFocus onClick={() => finish(false)}>
            Cancel
          </button>
          <button
            type="button"
            className={`btn ${pending?.danger ? 'btn-error' : 'btn-primary'}`}
            onClick={() => finish(true)}
          >
            {pending?.confirmLabel}
          </button>
        </div>
      </Modal>
    </ConfirmContext.Provider>
  )
}
