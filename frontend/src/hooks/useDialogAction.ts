import { useMutation } from '@tanstack/react-query'
import { errorMessage } from '../api'

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
