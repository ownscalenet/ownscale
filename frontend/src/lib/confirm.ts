import { createContext, useContext, type ReactNode } from 'react'

export interface ConfirmOptions {
  title: string
  message?: ReactNode
  // consequences lists what the action will do, in plain language.
  consequences?: string[]
  // confirmLabel names the action, e.g. "Remove", on the confirm button.
  confirmLabel: string
  // tone styles the dialog: "danger" for destructive actions, "public" for
  // ones that expose something to the internet.
  tone?: 'default' | 'danger' | 'public'
  // typeToConfirm is text the user must type before the action is enabled.
  typeToConfirm?: string
}

// Confirm asks the user to confirm an action and resolves to their answer.
export type Confirm = (options: ConfirmOptions) => Promise<boolean>

export const ConfirmContext = createContext<Confirm | null>(null)

// useConfirm returns the Confirm of the enclosing ConfirmProvider.
export function useConfirm(): Confirm {
  const confirm = useContext(ConfirmContext)
  if (!confirm) throw new Error('useConfirm needs a ConfirmProvider')
  return confirm
}
