import { diffLines } from 'diff'

// DiffModal shows a line-level diff between the saved and current content of
// a policy editor.
export default function DiffModal({
  dialogRef,
  savedContent,
  content,
}: {
  dialogRef: React.RefObject<HTMLDialogElement | null>
  savedContent: string
  content: string
}) {
  const parts = diffLines(savedContent, content)
  return (
    <dialog ref={dialogRef} className="modal">
      <div className="modal-box max-w-3xl">
        <h3 className="font-bold text-lg mb-2">Diff since last save</h3>
        <pre className="text-xs bg-base-200 rounded p-3 overflow-x-auto whitespace-pre-wrap max-h-[28rem] overflow-y-auto">
          {parts.length === 0 || parts.every((p) => !p.added && !p.removed) ? (
            <span className="text-base-content/60">No changes.</span>
          ) : (
            parts.map((part, i) => {
              const lines = part.value.endsWith('\n')
                ? part.value.slice(0, -1).split('\n')
                : part.value.split('\n')
              return (
                <span
                  key={i}
                  className={
                    part.added
                      ? 'bg-success/20 text-success-content block'
                      : part.removed
                        ? 'bg-error/20 text-error-content block'
                        : 'block'
                  }
                >
                  {lines.map((line, li) => (
                    <span key={li} className="block">
                      {part.added ? '+ ' : part.removed ? '- ' : '  '}
                      {line}
                    </span>
                  ))}
                </span>
              )
            })
          )}
        </pre>
        <div className="modal-action">
          <form method="dialog">
            <button className="btn">Close</button>
          </form>
        </div>
      </div>
      <form method="dialog" className="modal-backdrop">
        <button>close</button>
      </form>
    </dialog>
  )
}
