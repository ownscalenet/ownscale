import { useCallback, useRef, useState, type ReactNode } from 'react'
import { Globe, Info, TriangleAlert } from 'lucide-react'
import { ConfirmContext, type Confirm, type ConfirmOptions } from '../lib/confirm'
import Modal from './Modal'

const tones = {
  default: {
    bar: 'bg-primary',
    tile: 'bg-primary/15 text-primary',
    button: 'btn-primary',
    Icon: Info,
  },
  danger: {
    bar: 'bg-error',
    tile: 'bg-error/15 text-error',
    button: 'btn-error',
    Icon: TriangleAlert,
  },
  public: {
    bar: 'bg-warning',
    tile: 'bg-warning/15 text-warning',
    button: 'btn-warning',
    Icon: Globe,
  },
}

// ConfirmProvider renders the confirmation dialog that useConfirm asks for.
// The confirm button has the initial focus, matching the other modals' order.
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

  const tone = tones[pending?.tone ?? 'default']

  return (
    <ConfirmContext.Provider value={confirm}>
      {children}
      <Modal
        open={pending !== null}
        onClose={() => finish(false)}
        className="max-w-md overflow-hidden relative"
        title={
          <span className="flex items-center gap-3">
            <span className={`size-8 shrink-0 rounded-field grid place-items-center ${tone.tile}`}>
              <tone.Icon className="size-4" aria-hidden="true" />
            </span>
            {pending?.title}
          </span>
        }
      >
        <div className={`absolute inset-x-0 top-0 h-[3px] ${tone.bar}`} aria-hidden="true" />
        {pending?.message && <p>{pending.message}</p>}
        {pending?.consequences && (
          <ul className="list-disc pl-5 mt-2 text-sm text-base-content/80 space-y-1">
            {pending.consequences.map((c) => (
              <li key={c}>{c}</li>
            ))}
          </ul>
        )}
        <div className="modal-action">
          <button
            type="button"
            className={`btn ${tone.button}`}
            autoFocus
            onClick={() => finish(true)}
          >
            {pending?.confirmLabel}
          </button>
          <button type="button" className="btn btn-ghost" onClick={() => finish(false)}>
            Cancel
          </button>
        </div>
      </Modal>
    </ConfirmContext.Provider>
  )
}
