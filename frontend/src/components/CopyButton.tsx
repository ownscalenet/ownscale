import { Check, Copy, X } from 'lucide-react'
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
  const Icon = result === 'copied' ? Check : result === 'failed' ? X : Copy
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
        <Icon
          className={`size-3.5 ${
            result === 'copied'
              ? 'text-success'
              : result === 'failed'
                ? 'text-error'
                : 'opacity-60 hover:opacity-100'
          }`}
          aria-hidden="true"
        />
      </button>
      <span className="sr-only" aria-live="polite">
        {result ? tip : ''}
      </span>
    </span>
  )
}
