import { useEffect, useState, type MouseEvent, type ReactNode } from 'react'

type CopyResult = 'copied' | 'failed' | null

const shownMs = 2000
// daisyUI fades a tooltip out in 200ms after a 75ms delay.
const fadeMs = 300

// useCopy copies text to the clipboard and remembers the outcome for 2s.
// open turns false first and result is kept while the tooltip fades out, so
// the fading tooltip doesn't switch to its resting text and color.
// Copying fails on plain-HTTP origins, where the clipboard isn't available.
function useCopy(text: string) {
  const [result, setResult] = useState<CopyResult>(null)
  const [open, setOpen] = useState(false)

  useEffect(() => {
    if (!result) return
    const timer = open
      ? setTimeout(() => setOpen(false), shownMs)
      : setTimeout(() => setResult(null), fadeMs)
    return () => clearTimeout(timer)
  }, [result, open])

  const copy = () => {
    // navigator.clipboard is undefined on insecure origins.
    Promise.resolve()
      .then(() => navigator.clipboard.writeText(text))
      .then(
        () => setResult('copied'),
        () => setResult('failed'),
      )
      .then(() => setOpen(true))
  }

  return { result, open, copy }
}

// CopyButton copies text to the clipboard and briefly says whether it worked.
export default function CopyButton({
  text,
  className = 'btn',
  children,
}: {
  text: string
  className?: string
  children: ReactNode
}) {
  const { result, copy } = useCopy(text)

  return (
    <button type="button" className={className} onClick={copy}>
      {result === 'copied' ? 'Copied!' : result === 'failed' ? 'Copy failed' : children}
    </button>
  )
}

// CopyIcon is a small icon button for copying a table value. Its tooltip
// stays on top because tables sit in overflow containers that clip sideways.
export function CopyIcon({ text, label }: { text: string; label: string }) {
  const { result, open, copy } = useCopy(text)
  const tip = result === 'copied' ? 'Copied!' : result === 'failed' ? 'Copy failed' : 'Copy'
  const tooltipColor =
    result === 'copied' ? 'tooltip-success' : result === 'failed' ? 'tooltip-error' : ''

  const onClick = (e: MouseEvent) => {
    e.stopPropagation()
    copy()
  }

  return (
    <span
      className={`tooltip tooltip-top ${tooltipColor} ${open ? 'tooltip-open' : ''}`}
      data-tip={tip}
    >
      <button
        type="button"
        className="btn btn-ghost btn-xs btn-square"
        aria-label={`Copy ${label}`}
        onClick={onClick}
      >
        <svg
          xmlns="http://www.w3.org/2000/svg"
          className={`h-3.5 w-3.5 ${
            result === 'copied'
              ? 'text-success'
              : result === 'failed'
                ? 'text-error'
                : 'opacity-60 hover:opacity-100'
          }`}
          fill="none"
          viewBox="0 0 24 24"
          stroke="currentColor"
          strokeWidth={2}
          aria-hidden="true"
        >
          {result === 'copied' ? (
            <path strokeLinecap="round" strokeLinejoin="round" d="M5 13l4 4L19 7" />
          ) : result === 'failed' ? (
            <path strokeLinecap="round" strokeLinejoin="round" d="M6 6l12 12M18 6L6 18" />
          ) : (
            <>
              <rect x="9" y="9" width="11" height="11" rx="2" />
              <path strokeLinecap="round" strokeLinejoin="round" d="M5 15V5a2 2 0 0 1 2-2h8" />
            </>
          )}
        </svg>
      </button>
      <span className="sr-only" aria-live="polite">
        {result ? tip : ''}
      </span>
    </span>
  )
}
