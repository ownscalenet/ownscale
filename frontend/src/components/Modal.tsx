import { Dialog, DialogPanel, DialogTitle } from '@headlessui/react'
import type { ReactNode } from 'react'

// Modal is a Headless UI dialog styled as a daisyUI modal. Headless UI traps
// focus, closes it on Escape or a click outside, locks page scrolling and
// returns focus when it closes. Nothing is rendered while it's closed.
export default function Modal({
  open,
  onClose,
  title,
  className = '',
  children,
}: {
  open: boolean
  onClose: () => void
  title: ReactNode
  className?: string
  children: ReactNode
}) {
  return (
    <Dialog open={open} onClose={onClose} className="modal modal-open">
      <DialogPanel className={`modal-box text-left ${className}`}>
        <DialogTitle as="h3" className="text-lg font-bold mb-4">
          {title}
        </DialogTitle>
        {children}
      </DialogPanel>
    </Dialog>
  )
}
