import { useRef, type ChangeEvent, type UIEvent } from 'react'

// LineNumberedTextarea wraps a plain <textarea> with a scroll-synced line-
// number gutter. Lines don't wrap, so each one stays level with its number.
export default function LineNumberedTextarea({
  value,
  onChange,
  readOnly,
  spellCheck = false,
  minHeight = '32rem',
}: {
  value: string
  onChange: (value: string) => void
  readOnly?: boolean
  spellCheck?: boolean
  minHeight?: string
}) {
  const gutterRef = useRef<HTMLDivElement>(null)

  const lineCount = value === '' ? 1 : value.split('\n').length

  const syncGutterScroll = (e: UIEvent<HTMLTextAreaElement>) => {
    if (gutterRef.current) {
      gutterRef.current.scrollTop = e.currentTarget.scrollTop
    }
  }

  return (
    <div
      className="textarea textarea-bordered w-full p-0 flex overflow-hidden resize-y"
      style={{ minHeight }}
    >
      <div
        ref={gutterRef}
        aria-hidden="true"
        className="select-none text-right text-base-content/40 bg-base-200/50 px-2 py-2 font-mono text-sm overflow-hidden shrink-0"
        style={{ lineHeight: '1.25rem' }}
      >
        {Array.from({ length: lineCount }, (_, i) => (
          <div key={i}>{i + 1}</div>
        ))}
      </div>
      <textarea
        className="grow resize-none outline-none bg-transparent px-2 py-2 font-mono text-sm overflow-auto whitespace-pre"
        style={{ lineHeight: '1.25rem' }}
        wrap="off"
        spellCheck={spellCheck}
        value={value}
        readOnly={readOnly}
        onChange={(e: ChangeEvent<HTMLTextAreaElement>) => onChange(e.target.value)}
        onScroll={syncGutterScroll}
      />
    </div>
  )
}
